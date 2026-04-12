(() => {
    const { NAV_ITEMS } = window.GoProxyConstants;

    function createEmptyConfig() {
        return { port: 8080, log_request_params: false, routes: [], tcp_routes: [], ssh_tunnels: [] };
    }

    function createEmptyMetrics() {
        return {
            updated_at: '',
            http_requests: 0,
            http_active: 0,
            http_inbound_bytes: 0,
            http_outbound_bytes: 0,
            tcp_connections: 0,
            tcp_active: 0,
            tcp_inbound_bytes: 0,
            tcp_outbound_bytes: 0,
            http_routes: 0,
            http_enabled_routes: 0,
            tcp_routes: 0,
            tcp_enabled_routes: 0,
            ssh_tunnels: 0,
            system_cpu_percent: 0,
            system_memory_used: 0,
            system_memory_total: 0,
            system_memory_percent: 0,
            system_disk_used: 0,
            system_disk_total: 0,
            system_disk_percent: 0
        };
    }

    function defaultUpstream(mode) {
        return {
            target: mode === 'tcp' ? '10.0.0.1:3306' : 'http://127.0.0.1:3000',
            weight: 1,
            backup: false,
            enabled: true
        };
    }

    function createRouteForm() {
        return {
            path: '',
            priority: 0,
            type: 'proxy',
            strip_prefix: true,
            timeout: 0,
            enabled: true,
            upstreams: [defaultUpstream('http')],
            headers: [],
            mock: {
                method: 'ANY',
                status_code: 200,
                response_type: 'application/json',
                params: [],
                headers: [],
                dataText: JSON.stringify({ ok: true }, null, 2)
            }
        };
    }

    function createTcpForm() {
        return { listen: '', enabled: true, upstreams: [defaultUpstream('tcp')] };
    }

    function createSSHForm() {
        return {
            name: '',
            enabled: true,
            host: '',
            port: 22,
            username: '',
            auth_type: 'password',
            password: '',
            private_key: '',
            private_key_path: '',
            direction: 'local',
            local_address: '127.0.0.1:8080',
            remote_address: '127.0.0.1:80'
        };
    }

    function normalizeRouteType(type) {
        return String(type || 'proxy').toLowerCase() === 'mock' ? 'mock' : 'proxy';
    }

    function normalizeSection(section) {
        const value = String(section || '').replace(/^#/, '').trim().toLowerCase();
        const normalized = value === 'overview' ? 'dashboard' : value;
        return NAV_ITEMS.some(item => item.key === normalized) ? normalized : 'dashboard';
    }

    function isEnabled(item) {
        return item && item.enabled !== false;
    }

    function routeTargets(route) {
        if (Array.isArray(route?.upstreams) && route.upstreams.length) return route.upstreams;
        if (route?.target) return [{ target: route.target, weight: 1, backup: false, enabled: true }];
        return [];
    }

    function mapToPairs(input) {
        return Object.entries(input || {}).map(([key, value]) => ({ key, value: String(value ?? '') }));
    }

    function pairsToMap(pairs) {
        return (pairs || []).reduce((result, item) => {
            const key = String(item.key || '').trim();
            if (!key) return result;
            result[key] = String(item.value ?? '').trim();
            return result;
        }, {});
    }

    function normalizeUpstreams(upstreams, mode) {
        return (upstreams || []).filter(item => String(item.target || '').trim()).map(item => {
            let target = String(item.target || '').trim();
            if (mode === 'http' && !/^https?:\/\//.test(target)) target = 'http://' + target;
            return {
                target,
                weight: Number(item.weight) > 0 ? Number(item.weight) : 1,
                backup: !!item.backup,
                enabled: item.enabled !== false
            };
        });
    }

    function routeTypeLabel(route) {
        const type = normalizeRouteType(route?.type);
        return type === 'mock'
            ? { text: 'Mock', tint: 'bg-slate-900 text-blue-200' }
            : { text: 'Proxy', tint: 'bg-blue-600/15 text-blue-200' };
    }

    function routeSummary(route) {
        if (normalizeRouteType(route?.type) === 'mock') {
            const method = route?.mock?.method || 'ANY';
            const status = route?.mock?.status_code || 200;
            return method + ' / HTTP ' + status + ' / ' + (route?.mock?.response_type || 'application/json');
        }
        const targets = routeTargets(route).map(item => item.target).filter(Boolean);
        return targets.length ? targets.join(' | ') : '未配置目标';
    }

    function tcpSummary(route) {
        const targets = routeTargets(route).map(item => item.target).filter(Boolean);
        return targets.length ? targets.join(' | ') : '未配置目标';
    }

    function sshDirectionLabel(tunnel) {
        return tunnel?.direction === 'remote' ? '远程转发' : '本地转发';
    }

    function sshAuthLabel(tunnel) {
        return tunnel?.auth_type === 'key' ? '证书' : '密码';
    }

    function sshSummary(tunnel) {
        const server = `${tunnel.host || '--'}:${Number(tunnel.port) || 22}`;
        const local = tunnel.local_address || '--';
        const remote = tunnel.remote_address || '--';
        return `${server} / ${local} -> ${remote}`;
    }

    function average(values) {
        return values.reduce((sum, value) => sum + value, 0) / Math.max(values.length, 1);
    }

    function formatRate(value) {
        if (value >= 1024 * 1024 * 1024) return (value / (1024 * 1024 * 1024)).toFixed(2) + ' GB/s';
        if (value >= 1024 * 1024) return (value / (1024 * 1024)).toFixed(2) + ' MB/s';
        if (value >= 1024) return (value / 1024).toFixed(2) + ' KB/s';
        return Number(value || 0).toFixed(0) + ' B/s';
    }

    function formatBytes(value) {
        if (value >= 1024 * 1024 * 1024) return (value / (1024 * 1024 * 1024)).toFixed(2) + ' GB';
        if (value >= 1024 * 1024) return (value / (1024 * 1024)).toFixed(2) + ' MB';
        if (value >= 1024) return (value / 1024).toFixed(2) + ' KB';
        return value + ' B';
    }

    function seriesToPoints(series, width, height) {
        const max = Math.max(...series, 1);
        const min = Math.min(...series, 0);
        const innerHeight = height - 30;
        return series.map((value, index) => {
            const x = (width / (series.length - 1)) * index;
            const ratio = max === min ? 0.5 : (value - min) / (max - min);
            const y = innerHeight - ratio * (innerHeight - 20);
            return { x, y };
        });
    }

    function linePath(points) {
        if (!points.length) return '';
        let path = `M ${points[0].x},${points[0].y}`;
        for (let i = 0; i < points.length - 1; i += 1) {
            const current = points[i];
            const next = points[i + 1];
            const cp1x = current.x + (next.x - current.x) / 3;
            const cp2x = current.x + ((next.x - current.x) * 2) / 3;
            path += ` C ${cp1x},${current.y} ${cp2x},${next.y} ${next.x},${next.y}`;
        }
        return path;
    }

    function areaPath(points, width, height) {
        if (!points.length) return '';
        return `${linePath(points)} L ${width},${height} L 0,${height} Z`;
    }

    function clone(value) {
        return JSON.parse(JSON.stringify(value));
    }

    window.GoProxyUtils = {
        createEmptyConfig,
        createEmptyMetrics,
        defaultUpstream,
        createRouteForm,
        createTcpForm,
        createSSHForm,
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
        clone
    };
})();
