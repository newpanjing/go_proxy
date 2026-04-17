const { createApp } = Vue;
const { NAV_ITEMS, MODULE_META } = window.GoProxyConstants;
const {
    createEmptyConfig,
    createEmptyMetrics,
    createRouteForm,
    createTcpForm,
    createSSHForm,
    defaultUpstream,
    normalizeRouteType,
    normalizeSection,
    isEnabled,
    routeTargets,
    mapToPairs,
    pairsToMap,
    normalizeUpstreams,
    routeTypeLabel,
    routeSummary,
    tcpSummary,
    sshDirectionLabel,
    sshAuthLabel,
    sshSummary,
    average,
    formatRate,
    formatBytes,
    seriesToPoints,
    linePath,
    areaPath,
    clone,
    safeParseJson,
    formatJsonText
} = window.GoProxyUtils;
const { api, connectTelemetry, connectLogs } = window.GoProxyServices;
const { JsonCodeEditor, StatusSwitch, LogStreamPanel, TcpLogPanel } = window.GoProxyComponents;

createApp({
    components: {
        JsonCodeEditor,
        StatusSwitch,
        LogStreamPanel,
        TcpLogPanel
    },
    data() {
        return {
            activeSection: 'dashboard',
            theme: localStorage.getItem('go-proxy-theme') === 'light' ? 'light' : 'dark',
            config: createEmptyConfig(),
            metrics: createEmptyMetrics(),
            trafficHistory: [],
            lastMetricPoint: null,
            chartHover: null,
            sseConnected: false,
            eventSource: null,
            reconnectTimer: null,
            logEventSource: null,
            logReconnectTimer: null,
            logs: [],
            tcpLogsByListen: {},
            tcpLogModal: { open: false, listen: '' },
            floatingLogsOpen: false,
            floatingLogsCollapsed: false,
            sidebarCollapsed: localStorage.getItem('go-proxy-sidebar-collapsed') === '1',
            toastMessage: '',
            toastType: 'warning',
            toastTimer: null,
            routeModal: { open: false, originalPath: '', form: createRouteForm() },
            tcpModal: { open: false, originalListen: '', form: createTcpForm() },
            sshModal: { open: false, originalName: '', form: createSSHForm() },
            navItems: NAV_ITEMS,
            docSections: [
                { id: 'doc-start', label: '启动说明' },
                { id: 'doc-config', label: '配置参数' },
                { id: 'doc-api-config', label: '配置接口' },
                { id: 'doc-api-events', label: '事件接口' },
                { id: 'doc-api-routes', label: 'HTTP 接口' },
                { id: 'doc-api-tcp', label: 'TCP 接口' },
                { id: 'doc-api-ssh', label: 'SSH 接口' },
                { id: 'doc-api-logs', label: '日志接口' }
            ]
        };
    },
    computed: {
        activeModule() {
            const section = normalizeSection(this.activeSection);
            return MODULE_META[section] || MODULE_META.dashboard || { title: '仪表盘', description: '' };
        },
        activeTcpLogs() {
            return this.tcpLogsByListen[this.tcpLogModal.listen] || [];
        },
        enabledHttpRoutes() {
            return this.config.routes.filter(isEnabled);
        },
        enabledTcpRoutes() {
            return this.config.tcp_routes.filter(isEnabled);
        },
        trafficSeries() {
            const series = this.trafficHistory.slice(-12);
            if (!series.length) return { inbound: Array.from({ length: 12 }, () => 0), outbound: Array.from({ length: 12 }, () => 0) };
            const padded = Array.from({ length: Math.max(0, 12 - series.length) }, () => ({ inbound: 0, outbound: 0 }));
            const merged = [...padded, ...series];
            return { inbound: merged.map(item => item.inbound), outbound: merged.map(item => item.outbound) };
        },
        inboundPoints() {
            return seriesToPoints(this.trafficSeries.inbound, 680, 260);
        },
        outboundPoints() {
            return seriesToPoints(this.trafficSeries.outbound, 680, 260);
        },
        inboundDisplay() {
            const latest = this.trafficHistory[this.trafficHistory.length - 1];
            return formatRate(latest ? latest.inbound : 0);
        },
        outboundDisplay() {
            const latest = this.trafficHistory[this.trafficHistory.length - 1];
            return formatRate(latest ? latest.outbound : 0);
        },
        avgInbound() {
            return formatRate(average(this.trafficSeries.inbound));
        },
        avgOutbound() {
            return formatRate(average(this.trafficSeries.outbound));
        },
        hoverInboundValue() {
            if (!this.chartHover) return '';
            return formatRate(this.trafficSeries.inbound[this.chartHover.index] || 0);
        },
        hoverOutboundValue() {
            if (!this.chartHover) return '';
            return formatRate(this.trafficSeries.outbound[this.chartHover.index] || 0);
        },
        totalTrafficBytes() {
            return (this.metrics.http_inbound_bytes || 0) + (this.metrics.http_outbound_bytes || 0) + (this.metrics.tcp_inbound_bytes || 0) + (this.metrics.tcp_outbound_bytes || 0);
        },
        totalMemoryDisplay() {
            return `${formatBytes(this.metrics.system_memory_used || 0)} / ${formatBytes(this.metrics.system_memory_total || 0)}`;
        },
        totalDiskDisplay() {
            return `${formatBytes(this.metrics.system_disk_used || 0)} / ${formatBytes(this.metrics.system_disk_total || 0)}`;
        },
        statCards() {
            const activeTunnels = (this.metrics.http_enabled_routes || 0) + (this.metrics.tcp_enabled_routes || 0) + (this.metrics.ssh_tunnels || 0);
            return [
                {
                    label: 'CPU',
                    value: `${Number(this.metrics.system_cpu_percent || 0).toFixed(1)}%`,
                    hint: '当前服务器 CPU 占用',
                    badge: 'Host',
                    percent: Number(this.metrics.system_cpu_percent || 0),
                    chart: true,
                    tint: 'border-blue-500/20 bg-blue-600/15 text-blue-200',
                    footerLeft: 'Memory ' + `${Number(this.metrics.system_memory_percent || 0).toFixed(1)}%`,
                    footerRight: 'Disk ' + `${Number(this.metrics.system_disk_percent || 0).toFixed(1)}%`
                },
                {
                    label: 'Memory',
                    value: this.totalMemoryDisplay,
                    hint: '当前服务器内存使用情况',
                    badge: `${Number(this.metrics.system_memory_percent || 0).toFixed(1)}%`,
                    percent: Number(this.metrics.system_memory_percent || 0),
                    chart: true,
                    tint: 'border-blue-500/20 bg-blue-600/15 text-blue-200',
                    footerLeft: 'Used ' + formatBytes(this.metrics.system_memory_used || 0),
                    footerRight: 'Total ' + formatBytes(this.metrics.system_memory_total || 0)
                },
                {
                    label: 'Disk',
                    value: this.totalDiskDisplay,
                    hint: '当前工作目录所在磁盘使用情况',
                    badge: `${Number(this.metrics.system_disk_percent || 0).toFixed(1)}%`,
                    percent: Number(this.metrics.system_disk_percent || 0),
                    chart: true,
                    tint: 'border-blue-500/20 bg-blue-600/15 text-blue-200',
                    footerLeft: 'Used ' + formatBytes(this.metrics.system_disk_used || 0),
                    footerRight: 'Total ' + formatBytes(this.metrics.system_disk_total || 0)
                },
                {
                    label: 'HTTP Requests',
                    value: String(this.metrics.http_requests || 0),
                    hint: '真实请求数，来自代理处理完成后的服务端计数',
                    badge: this.metrics.http_active > 0 ? 'Active' : 'Idle',
                    tint: this.metrics.http_active > 0 ? 'border-blue-500/20 bg-blue-600/15 text-blue-200' : 'border-slate-700 bg-slate-900 text-slate-300',
                    footerLeft: (this.metrics.http_enabled_routes || 0) + ' HTTP enabled',
                    footerRight: (this.metrics.http_active || 0) + ' inflight'
                },
                {
                    label: 'Total Traffic',
                    value: formatBytes(this.totalTrafficBytes),
                    hint: 'HTTP + TCP 双向累计字节数',
                    badge: 'SSE',
                    tint: 'border-blue-500/20 bg-blue-600/15 text-blue-200',
                    footerLeft: 'Inbound ' + formatBytes((this.metrics.http_inbound_bytes || 0) + (this.metrics.tcp_inbound_bytes || 0)),
                    footerRight: 'Outbound ' + formatBytes((this.metrics.http_outbound_bytes || 0) + (this.metrics.tcp_outbound_bytes || 0))
                },
                {
                    label: 'Active Tunnels',
                    value: String(activeTunnels).padStart(2, '0'),
                    hint: '按配置启用状态统计的 HTTP / TCP / SSH 通道数',
                    badge: activeTunnels > 0 ? 'Running' : 'Idle',
                    tint: activeTunnels > 0 ? 'border-blue-500/20 bg-blue-600/15 text-blue-200' : 'border-slate-700 bg-slate-900 text-slate-300',
                    footerLeft: (this.metrics.tcp_connections || 0) + ' TCP sessions',
                    footerRight: (this.metrics.tcp_active || 0) + ' active now'
                }
            ];
        }
    },
    mounted() {
        this.applyTheme();
        this.syncSectionFromHash();
        this.loadConfig();
        connectTelemetry(this);
        connectLogs(this);
        window.addEventListener('hashchange', this.syncSectionFromHash);
        this.scrollTimers = new WeakMap();
        window.addEventListener('scroll', this.handleGlobalScroll, true);
    },
    beforeUnmount() {
        clearTimeout(this.toastTimer);
        clearTimeout(this.reconnectTimer);
        clearTimeout(this.logReconnectTimer);
        window.removeEventListener('hashchange', this.syncSectionFromHash);
        window.removeEventListener('scroll', this.handleGlobalScroll, true);
        if (this.eventSource) this.eventSource.close();
        if (this.logEventSource) this.logEventSource.close();
    },
    methods: {
        linePath,
        areaPath,
        routeTypeLabel,
        routeSummary,
        tcpSummary,
        sshDirectionLabel,
        sshAuthLabel,
        sshSummary,
        routeTargets,
        isEnabled,
        formatBytes,
        formatRate,
        navigateTo(section) {
            const next = normalizeSection(section);
            this.activeSection = next;
            if (window.location.hash !== '#' + next) window.location.hash = next;
        },
        syncSectionFromHash() {
            this.activeSection = normalizeSection(window.location.hash);
        },
        async loadConfig() {
            try {
                this.config = await api.loadConfig();
            } catch {
                this.showToast('加载配置失败', 'warning');
            }
        },
        async saveConfig() {
            try {
                await api.saveConfig(this.config);
                this.showToast('配置已保存', 'success');
            } catch {
                this.showToast('保存配置失败', 'warning');
            }
        },
        applyTheme() {
            const root = document.documentElement;
            root.classList.toggle('dark', this.theme === 'dark');
            root.classList.toggle('light', this.theme === 'light');
        },
        toggleTheme() {
            this.theme = this.theme === 'dark' ? 'light' : 'dark';
            localStorage.setItem('go-proxy-theme', this.theme);
            this.applyTheme();
        },
        applyMetrics(snapshot) {
            this.metrics = snapshot;
            const now = new Date(snapshot.updated_at || Date.now()).getTime();
            const totalInbound = (snapshot.http_inbound_bytes || 0) + (snapshot.tcp_inbound_bytes || 0);
            const totalOutbound = (snapshot.http_outbound_bytes || 0) + (snapshot.tcp_outbound_bytes || 0);
            if (this.lastMetricPoint) {
                const deltaMs = Math.max(now - this.lastMetricPoint.time, 1);
                const seconds = deltaMs / 1000;
                this.trafficHistory.push({
                    inbound: Math.max(0, (totalInbound - this.lastMetricPoint.inbound) / seconds),
                    outbound: Math.max(0, (totalOutbound - this.lastMetricPoint.outbound) / seconds)
                });
                this.trafficHistory = this.trafficHistory.slice(-24);
            } else {
                this.trafficHistory.push({ inbound: 0, outbound: 0 });
            }
            this.lastMetricPoint = { time: now, inbound: totalInbound, outbound: totalOutbound };
        },
        handleChartMove(event) {
            const svg = event.currentTarget;
            if (!svg || !this.inboundPoints.length) return;
            const rect = svg.getBoundingClientRect();
            if (!rect.width) return;
            const x = ((event.clientX - rect.left) / rect.width) * 680;
            let nearestIndex = 0;
            let nearestDistance = Infinity;
            this.inboundPoints.forEach((point, index) => {
                const distance = Math.abs(point.x - x);
                if (distance < nearestDistance) {
                    nearestDistance = distance;
                    nearestIndex = index;
                }
            });
            const inboundPoint = this.inboundPoints[nearestIndex];
            const outboundPoint = this.outboundPoints[nearestIndex];
            this.chartHover = {
                index: nearestIndex,
                x: inboundPoint.x,
                inboundY: inboundPoint.y,
                outboundY: outboundPoint ? outboundPoint.y : inboundPoint.y
            };
        },
        clearChartHover() {
            this.chartHover = null;
        },
        handleGlobalScroll(event) {
            const target = event.target;
            if (!(target instanceof HTMLElement) || !target.classList.contains('ui-scroll')) return;
            target.classList.add('scrolling');
            const current = this.scrollTimers.get(target);
            if (current) clearTimeout(current);
            const timer = setTimeout(() => {
                target.classList.remove('scrolling');
            }, 720);
            this.scrollTimers.set(target, timer);
        },
        scrollDoc(id) {
            const target = document.getElementById(id);
            if (!target) return;
            target.scrollIntoView({ behavior: 'smooth', block: 'start' });
        },
        circleDash(percent) {
            const radius = 30;
            const circumference = 2 * Math.PI * radius;
            const safe = Math.max(0, Math.min(Number(percent) || 0, 100));
            return `${(safe / 100) * circumference} ${circumference}`;
        },
        showToast(message, type = 'warning') {
            this.toastMessage = message;
            this.toastType = type;
            clearTimeout(this.toastTimer);
            this.toastTimer = setTimeout(() => {
                this.toastMessage = '';
            }, 2400);
        },
        toggleSidebar() {
            this.sidebarCollapsed = !this.sidebarCollapsed;
            localStorage.setItem('go-proxy-sidebar-collapsed', this.sidebarCollapsed ? '1' : '0');
        },
        openFloatingLogs() {
            this.floatingLogsOpen = true;
            this.floatingLogsCollapsed = false;
        },
        closeFloatingLogs() {
            this.floatingLogsOpen = false;
            this.floatingLogsCollapsed = false;
        },
        toggleFloatingLogsCollapsed() {
            if (!this.floatingLogsOpen) {
                this.floatingLogsOpen = true;
                this.floatingLogsCollapsed = false;
                return;
            }
            this.floatingLogsCollapsed = !this.floatingLogsCollapsed;
        },
        resetRouteForm() {
            this.routeModal.form = createRouteForm();
        },
        resetTcpForm() {
            this.tcpModal.form = createTcpForm();
        },
        resetSSHForm() {
            this.sshModal.form = createSSHForm();
        },
        openRouteModal(route) {
            this.resetRouteForm();
            this.routeModal.originalPath = route?.path || '';
            if (route) {
                const form = this.routeModal.form;
                form.path = route.path || '';
                form.priority = route.priority || 0;
                form.type = normalizeRouteType(route.type);
                form.strip_prefix = route.strip_prefix !== false;
                form.timeout = route.timeout || 0;
                form.enabled = isEnabled(route);
                form.upstreams = routeTargets(route).map(item => ({ ...item }));
                form.headers = mapToPairs(route.headers);
                form.mock = {
                    method: route.mock?.method || 'ANY',
                    status_code: route.mock?.status_code || 200,
                    response_type: route.mock?.response_type || 'application/json',
                    params: mapToPairs(route.mock?.params),
                    headers: mapToPairs(route.mock?.headers),
                    dataText: formatJsonText(route.mock?.data, {})
                };
                if (!form.upstreams.length) form.upstreams = [defaultUpstream('http')];
            }
            this.routeModal.open = true;
        },
        closeRouteModal() {
            this.routeModal.open = false;
        },
        openTcpModal(route) {
            this.resetTcpForm();
            this.tcpModal.originalListen = route?.listen || '';
            if (route) {
                this.tcpModal.form.listen = route.listen || '';
                this.tcpModal.form.enabled = isEnabled(route);
                this.tcpModal.form.upstreams = routeTargets(route).map(item => ({ ...item }));
                if (!this.tcpModal.form.upstreams.length) this.tcpModal.form.upstreams = [defaultUpstream('tcp')];
            }
            this.tcpModal.open = true;
        },
        closeTcpModal() {
            this.tcpModal.open = false;
        },
        openSSHModal(tunnel) {
            this.resetSSHForm();
            this.sshModal.originalName = tunnel?.name || '';
            if (tunnel) {
                Object.assign(this.sshModal.form, clone(tunnel));
            } else {
                this.sshModal.form.port = 22;
                this.sshModal.form.direction = 'local';
                this.sshModal.form.auth_type = 'password';
            }
            this.sshModal.open = true;
        },
        closeSSHModal() {
            this.sshModal.open = false;
        },
        openTcpLogs(route) {
            this.tcpLogModal.listen = route.listen || '';
            this.tcpLogModal.open = true;
        },
        closeTcpLogs() {
            this.tcpLogModal.open = false;
        },
        appendLogEntry(entry) {
            this.logs.push(entry);
            this.logs = this.logs.slice(-100);
            if (entry.kind === 'tcp' && entry.listen) {
                const current = this.tcpLogsByListen[entry.listen] || [];
                this.tcpLogsByListen = {
                    ...this.tcpLogsByListen,
                    [entry.listen]: [...current, entry].slice(-100)
                };
            }
        },
        async saveSSHTunnel() {
            const form = this.sshModal.form;
            const payload = {
                name: String(form.name || '').trim(),
                enabled: form.enabled !== false,
                host: String(form.host || '').trim(),
                port: Number(form.port) || 22,
                username: String(form.username || '').trim(),
                auth_type: form.auth_type === 'key' ? 'key' : 'password',
                password: String(form.password || ''),
                private_key: String(form.private_key || ''),
                private_key_path: String(form.private_key_path || '').trim(),
                direction: form.direction === 'remote' ? 'remote' : 'local',
                local_address: String(form.local_address || '').trim(),
                remote_address: String(form.remote_address || '').trim()
            };
            if (!payload.host) return this.showToast('请填写 SSH 主机', 'warning');
            if (!payload.username) return this.showToast('请填写用户名', 'warning');
            if (!payload.local_address) return this.showToast('请填写本地地址', 'warning');
            if (!payload.remote_address) return this.showToast('请填写远程地址', 'warning');
            if (payload.auth_type === 'password' && !payload.password) return this.showToast('请填写密码', 'warning');
            if (payload.auth_type === 'key' && !payload.private_key && !payload.private_key_path) return this.showToast('请填写私钥或私钥路径', 'warning');
            if (!payload.name) payload.name = `${payload.host}:${payload.port}-${payload.direction}`;
            try {
                const isEdit = !!this.sshModal.originalName;
                const result = await api.saveSSHTunnel(isEdit, this.sshModal.originalName, payload);
                if (result.error) return this.showToast(result.error, 'warning');
                this.closeSSHModal();
                await this.loadConfig();
                this.showToast(isEdit ? 'SSH 隧道已更新' : 'SSH 隧道已添加', 'success');
            } catch {
                this.showToast('保存 SSH 隧道失败', 'warning');
            }
        },
        async deleteSSHTunnel(name) {
            if (!confirm('确定删除 SSH 隧道 ' + name + '？')) return;
            try {
                await api.deleteSSHTunnel(name);
                await this.loadConfig();
                this.showToast('SSH 隧道已删除', 'success');
            } catch {
                this.showToast('删除 SSH 隧道失败', 'warning');
            }
        },
        async toggleSSHTunnelEnabled(tunnel) {
            try {
                const payload = clone(tunnel);
                payload.enabled = !isEnabled(tunnel);
                await api.updateSSHTunnel(tunnel.name, payload);
                await this.loadConfig();
            } catch {
                this.showToast('切换 SSH 隧道状态失败', 'warning');
            }
        },
        async saveRoute() {
            const form = this.routeModal.form;
            const path = String(form.path || '').trim();
            if (!path || !path.startsWith('/')) return this.showToast('路径必须以 / 开头', 'warning');

            let payload;
            if (form.type === 'mock') {
                const parsed = safeParseJson(form.mock.dataText, {});
                if (!parsed.ok) return this.showToast('响应数据必须是合法 JSON', 'warning');
                payload = {
                    path,
                    priority: Number(form.priority) || 0,
                    type: 'mock',
                    enabled: form.enabled !== false,
                    mock: {
                        method: form.mock.method || 'ANY',
                        params: pairsToMap(form.mock.params),
                        status_code: Number(form.mock.status_code) || 200,
                        response_type: form.mock.response_type || 'application/json',
                        headers: pairsToMap(form.mock.headers),
                        data: parsed.value
                    }
                };
            } else {
                const upstreams = normalizeUpstreams(form.upstreams, 'http');
                if (!upstreams.length) return this.showToast('请至少配置一个 Upstream', 'warning');
                payload = {
                    path,
                    priority: Number(form.priority) || 0,
                    type: 'proxy',
                    strip_prefix: form.strip_prefix !== false,
                    timeout: Number(form.timeout) || 0,
                    enabled: form.enabled !== false,
                    upstreams,
                    headers: pairsToMap(form.headers)
                };
            }
            try {
                const isEdit = !!this.routeModal.originalPath;
                const result = await api.saveRoute(isEdit, this.routeModal.originalPath, payload);
                if (result.error) return this.showToast(result.error, 'warning');
                this.closeRouteModal();
                await this.loadConfig();
                this.showToast(isEdit ? 'HTTP 路由已更新' : 'HTTP 路由已添加', 'success');
            } catch {
                this.showToast('保存路由失败', 'warning');
            }
        },
        async deleteRoute(path) {
            if (!confirm('确定删除路由 ' + path + '？')) return;
            try {
                await api.deleteRoute(path);
                await this.loadConfig();
                this.showToast('HTTP 路由已删除', 'success');
            } catch {
                this.showToast('删除路由失败', 'warning');
            }
        },
        async toggleRouteEnabled(route) {
            try {
                const payload = clone(route);
                payload.enabled = !isEnabled(route);
                await api.updateRoute(route.path, payload);
                await this.loadConfig();
            } catch {
                this.showToast('切换状态失败', 'warning');
            }
        },
        async saveTcpRoute() {
            const form = this.tcpModal.form;
            const listen = String(form.listen || '').trim();
            if (!listen) return this.showToast('请填写监听地址', 'warning');
            const upstreams = normalizeUpstreams(form.upstreams, 'tcp');
            if (!upstreams.length) return this.showToast('请至少配置一个 TCP Upstream', 'warning');
            const payload = { listen, enabled: form.enabled !== false, upstreams };
            try {
                const isEdit = !!this.tcpModal.originalListen;
                const result = await api.saveTcpRoute(isEdit, this.tcpModal.originalListen, payload);
                if (result.error) return this.showToast(result.error, 'warning');
                this.closeTcpModal();
                await this.loadConfig();
                this.showToast(isEdit ? 'TCP 转发已更新' : 'TCP 转发已添加', 'success');
            } catch {
                this.showToast('保存 TCP 转发失败', 'warning');
            }
        },
        async deleteTcpRoute(listen) {
            if (!confirm('确定删除 TCP 转发 ' + listen + '？')) return;
            try {
                await api.deleteTcpRoute(listen);
                await this.loadConfig();
                this.showToast('TCP 转发已删除', 'success');
            } catch {
                this.showToast('删除 TCP 转发失败', 'warning');
            }
        },
        async toggleTcpEnabled(route) {
            try {
                const payload = clone(route);
                payload.enabled = !isEnabled(route);
                await api.updateTcpRoute(route.listen, payload);
                await this.loadConfig();
            } catch {
                this.showToast('切换状态失败', 'warning');
            }
        }
    }
}).mount('#app');
