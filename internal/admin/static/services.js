(() => {
    async function consumeSSE(url, signal, onEvent, onOpen) {
        const response = await fetch(url, {
            headers: {
                Accept: 'text/event-stream',
                'Cache-Control': 'no-cache'
            },
            cache: 'no-store',
            signal
        });
        if (!response.ok || !response.body) {
            throw new Error('sse connect failed');
        }
        if (onOpen) onOpen();

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
            const { value, done } = await reader.read();
            if (done) {
                throw new Error('sse stream closed');
            }
            buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, '\n');

            let index = buffer.indexOf('\n\n');
            while (index !== -1) {
                const block = buffer.slice(0, index);
                buffer = buffer.slice(index + 2);
                parseSSEBlock(block, onEvent);
                index = buffer.indexOf('\n\n');
            }
        }
    }

    function parseSSEBlock(block, onEvent) {
        if (!block || block.startsWith(':')) return;
        let eventName = 'message';
        const dataLines = [];
        block.split('\n').forEach(line => {
            if (line.startsWith('event:')) {
                eventName = line.slice(6).trim();
                return;
            }
            if (line.startsWith('data:')) {
                dataLines.push(line.slice(5).trim());
            }
        });
        if (dataLines.length) {
            onEvent(eventName, dataLines.join('\n'));
        }
    }

    const api = {
        async loadConfig() {
            const response = await fetch('/api/config');
            return response.json();
        },
        async saveConfig(config) {
            return fetch('/api/config', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(config)
            });
        },
        async saveRoute(isEdit, originalPath, route) {
            const url = isEdit ? '/api/routes/update' : '/api/routes/add';
            const body = isEdit ? { original_path: originalPath, route } : route;
            const response = await fetch(url, {
                method: isEdit ? 'PUT' : 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body)
            });
            return response.json();
        },
        async deleteRoute(path) {
            return fetch('/api/routes/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ path })
            });
        },
        async updateRoute(originalPath, route) {
            return fetch('/api/routes/update', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ original_path: originalPath, route })
            });
        },
        async saveTcpRoute(isEdit, originalListen, route) {
            const url = isEdit ? '/api/tcp-routes/update' : '/api/tcp-routes/add';
            const body = isEdit ? { original_listen: originalListen, route } : route;
            const response = await fetch(url, {
                method: isEdit ? 'PUT' : 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body)
            });
            return response.json();
        },
        async deleteTcpRoute(listen) {
            return fetch('/api/tcp-routes/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ listen })
            });
        },
        async updateTcpRoute(originalListen, route) {
            return fetch('/api/tcp-routes/update', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ original_listen: originalListen, route })
            });
        },
        async saveSSHTunnel(isEdit, originalName, tunnel) {
            const url = isEdit ? '/api/ssh-tunnels/update' : '/api/ssh-tunnels/add';
            const body = isEdit ? { original_name: originalName, tunnel } : tunnel;
            const response = await fetch(url, {
                method: isEdit ? 'PUT' : 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body)
            });
            return response.json();
        },
        async deleteSSHTunnel(name) {
            return fetch('/api/ssh-tunnels/delete', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name })
            });
        },
        async updateSSHTunnel(originalName, tunnel) {
            return fetch('/api/ssh-tunnels/update', {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ original_name: originalName, tunnel })
            });
        }
    };

    function connectTelemetry(vm) {
        if (vm.eventSource) vm.eventSource.close();
        const controller = new AbortController();
        vm.eventSource = { close: () => controller.abort() };

        consumeSSE('/api/events', controller.signal, (eventName, data) => {
            if (eventName !== 'metrics') return;
            vm.sseConnected = true;
            vm.applyMetrics(JSON.parse(data));
        }, () => {
            vm.sseConnected = true;
        }).catch(() => {
            if (controller.signal.aborted) return;
            vm.sseConnected = false;
            clearTimeout(vm.reconnectTimer);
            vm.reconnectTimer = setTimeout(() => connectTelemetry(vm), 2000);
        });
    }

    function connectLogs(vm) {
        if (vm.logEventSource) vm.logEventSource.close();
        const controller = new AbortController();
        vm.logEventSource = { close: () => controller.abort() };

        consumeSSE('/api/logs/events', controller.signal, (eventName, data) => {
            if (eventName !== 'log') return;
            vm.appendLogEntry(JSON.parse(data));
        }).catch(() => {
            if (controller.signal.aborted) return;
            clearTimeout(vm.logReconnectTimer);
            vm.logReconnectTimer = setTimeout(() => connectLogs(vm), 2000);
        });
    }

    window.GoProxyServices = { api, connectTelemetry, connectLogs };
})();
