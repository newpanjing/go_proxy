window.GoProxyConstants = {
    NAV_ITEMS: [
        { key: 'dashboard', label: '仪表盘', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 13h7V4H4zM13 20h7v-9h-7zM13 4h7v7h-7zM4 16h7v4H4z"/></svg>' },
        { key: 'http', label: 'HTTP', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M4 12h16M12 4v16"/><circle cx="12" cy="12" r="9"/></svg>' },
        { key: 'tcp', label: 'TCP', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M6 6h12v5H6zM9 11v7m6-7v7M5 18h14"/></svg>' },
        { key: 'ssh', label: 'SSH', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M7 7h10v10H7z"/><path d="M9 12h6M12 9v6"/></svg>' },
        { key: 'logs', label: '日志', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M5 7h14M5 12h14M5 17h10"/></svg>' },
        { key: 'docs', label: '文档', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M7 4h8l4 4v12H7z"/><path d="M15 4v4h4"/><path d="M10 12h6M10 16h6"/></svg>' },
        { key: 'settings', label: '设置', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8"><path d="M12 8.5a3.5 3.5 0 1 0 0 7 3.5 3.5 0 0 0 0-7Z"/><path d="M19.4 15a1 1 0 0 0 .2 1.1l.1.1a2 2 0 0 1-2.8 2.8l-.1-.1a1 1 0 0 0-1.1-.2 1 1 0 0 0-.6.9V20a2 2 0 0 1-4 0v-.2a1 1 0 0 0-.6-.9 1 1 0 0 0-1.1.2l-.1.1a2 2 0 0 1-2.8-2.8l.1-.1a1 1 0 0 0 .2-1.1 1 1 0 0 0-.9-.6H4a2 2 0 0 1 0-4h.2a1 1 0 0 0 .9-.6 1 1 0 0 0-.2-1.1l-.1-.1a2 2 0 0 1 2.8-2.8l.1.1a1 1 0 0 0 1.1.2 1 1 0 0 0 .6-.9V4a2 2 0 0 1 4 0v.2a1 1 0 0 0 .6.9 1 1 0 0 0 1.1-.2l.1-.1a2 2 0 0 1 2.8 2.8l-.1.1a1 1 0 0 0-.2 1.1 1 1 0 0 0 .9.6h.2a2 2 0 0 1 0 4h-.2a1 1 0 0 0-.9.6Z"/></svg>' }
    ],
    LIST_TABS: [
        { key: 'dashboard', label: '仪表盘' },
        { key: 'http', label: 'HTTP' },
        { key: 'tcp', label: 'TCP' },
        { key: 'ssh', label: 'SSH' },
        { key: 'logs', label: '日志' },
        { key: 'docs', label: '文档' },
        { key: 'settings', label: '设置' }
    ],
    MODULE_META: {
        dashboard: { title: '仪表盘', description: '状态、资源监控与吞吐图表。' },
        http: { title: 'HTTP', description: 'HTTP 路由列表与操作。' },
        tcp: { title: 'TCP', description: 'TCP 转发链路与操作。' },
        ssh: { title: 'SSH 隧道', description: '这里只展示 SSH 隧道模块的当前状态。' },
        logs: { title: '日志', description: '通过 SSE 接收实时日志，前端只保留最近 100 条。' },
        docs: { title: '文档', description: '启动说明、配置参数与管理 API 接口说明。' },
        settings: { title: '设置', description: '全局配置与主题选项。' }
    }
};
