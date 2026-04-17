(() => {
    function request(url, options = {}) {
        const baseHeaders = options.body ? { 'Content-Type': 'application/json' } : {};
        return fetch(url, {
            ...options,
            headers: {
                ...baseHeaders,
                ...(options.headers || {})
            }
        });
    }

    async function requestJson(url, options = {}) {
        const response = await request(url, options);
        return response.json();
    }

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
            return requestJson('/api/config');
        },
        async saveConfig(config) {
            return request('/api/config', {
                method: 'PUT',
                body: JSON.stringify(config)
            });
        },
        async saveRoute(isEdit, originalPath, route) {
            const url = isEdit ? '/api/routes/update' : '/api/routes/add';
            const body = isEdit ? { original_path: originalPath, route } : route;
            return requestJson(url, {
                method: isEdit ? 'PUT' : 'POST',
                body: JSON.stringify(body)
            });
        },
        async deleteRoute(path) {
            return request('/api/routes/delete', {
                method: 'POST',
                body: JSON.stringify({ path })
            });
        },
        async updateRoute(originalPath, route) {
            return request('/api/routes/update', {
                method: 'PUT',
                body: JSON.stringify({ original_path: originalPath, route })
            });
        },
        async saveTcpRoute(isEdit, originalListen, route) {
            const url = isEdit ? '/api/tcp-routes/update' : '/api/tcp-routes/add';
            const body = isEdit ? { original_listen: originalListen, route } : route;
            return requestJson(url, {
                method: isEdit ? 'PUT' : 'POST',
                body: JSON.stringify(body)
            });
        },
        async deleteTcpRoute(listen) {
            return request('/api/tcp-routes/delete', {
                method: 'POST',
                body: JSON.stringify({ listen })
            });
        },
        async updateTcpRoute(originalListen, route) {
            return request('/api/tcp-routes/update', {
                method: 'PUT',
                body: JSON.stringify({ original_listen: originalListen, route })
            });
        },
        async saveSSHTunnel(isEdit, originalName, tunnel) {
            const url = isEdit ? '/api/ssh-tunnels/update' : '/api/ssh-tunnels/add';
            const body = isEdit ? { original_name: originalName, tunnel } : tunnel;
            return requestJson(url, {
                method: isEdit ? 'PUT' : 'POST',
                body: JSON.stringify(body)
            });
        },
        async deleteSSHTunnel(name) {
            return request('/api/ssh-tunnels/delete', {
                method: 'POST',
                body: JSON.stringify({ name })
            });
        },
        async updateSSHTunnel(originalName, tunnel) {
            return request('/api/ssh-tunnels/update', {
                method: 'PUT',
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
