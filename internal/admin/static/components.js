(() => {
const { safeParseJson, formatJsonText, minifyJsonText } = window.GoProxyUtils;

window.GoProxyComponents = {
    JsonCodeEditor: {
        props: {
            modelValue: { type: String, default: '' },
            rows: { type: Number, default: 14 }
        },
        emits: ['update:modelValue'],
        computed: {
            validation() {
                return safeParseJson(this.modelValue, {});
            },
            highlightedHtml() {
                const text = String(this.modelValue || '');
                if (!text) return '<span class="json-editor-placeholder">{\n  \n}</span>';
                const escaped = this.escapeHtml(text);
                return escaped
                    .replace(/("(?:\\.|[^"\\])*")(\s*:)?/g, (_, stringToken, colon) => {
                        const suffix = colon ? '<span class="json-editor-punctuation">:</span>' : '';
                        return `<span class="json-editor-string">${stringToken}</span>${suffix}`;
                    })
                    .replace(/\b(true|false)\b/g, '<span class="json-editor-boolean">$1</span>')
                    .replace(/\bnull\b/g, '<span class="json-editor-null">null</span>')
                    .replace(/-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/g, '<span class="json-editor-number">$&</span>')
                    .replace(/([{}[\],])/g, '<span class="json-editor-punctuation">$1</span>');
            }
        },
        methods: {
            escapeHtml(value) {
                return String(value || '')
                    .replace(/&/g, '&amp;')
                    .replace(/</g, '&lt;')
                    .replace(/>/g, '&gt;');
            },
            updateValue(event) {
                this.$emit('update:modelValue', event.target.value);
                this.syncScroll(event);
            },
            syncScroll(event) {
                const overlay = this.$refs.overlay;
                const input = event?.target || this.$refs.input;
                if (!overlay || !input) return;
                overlay.scrollTop = input.scrollTop;
                overlay.scrollLeft = input.scrollLeft;
            },
            setValue(nextValue) {
                this.$emit('update:modelValue', nextValue);
            },
            formatJson() {
                if (!this.validation.ok) return;
                this.setValue(formatJsonText(this.modelValue, {}));
            },
            minifyJson() {
                if (!this.validation.ok) return;
                this.setValue(minifyJsonText(this.modelValue, {}));
            },
            handleKeydown(event) {
                if (event.key !== 'Tab') return;
                event.preventDefault();
                const input = event.target;
                const value = input.value;
                const start = input.selectionStart;
                const end = input.selectionEnd;
                const indent = '  ';

                if (event.shiftKey) {
                    const lineStart = value.lastIndexOf('\n', Math.max(start - 1, 0)) + 1;
                    const selected = value.slice(lineStart, end);
                    const lines = selected.split('\n');
                    let removed = 0;
                    const next = lines.map(line => {
                        if (line.startsWith(indent)) {
                            removed += indent.length;
                            return line.slice(indent.length);
                        }
                        if (line.startsWith('\t')) {
                            removed += 1;
                            return line.slice(1);
                        }
                        return line;
                    }).join('\n');
                    const updated = value.slice(0, lineStart) + next + value.slice(end);
                    this.setValue(updated);
                    this.$nextTick(() => {
                        input.selectionStart = start > lineStart ? Math.max(lineStart, start - indent.length) : start;
                        input.selectionEnd = Math.max(input.selectionStart, end - removed);
                        this.syncScroll({ target: input });
                    });
                    return;
                }

                const updated = value.slice(0, start) + indent + value.slice(end);
                this.setValue(updated);
                this.$nextTick(() => {
                    input.selectionStart = input.selectionEnd = start + indent.length;
                    this.syncScroll({ target: input });
                });
            }
        },
        template: `
            <div class="json-editor-shell">
                <div class="json-editor-toolbar">
                    <div class="json-editor-status" :class="validation.ok ? 'is-valid' : 'is-invalid'">
                        {{ validation.ok ? 'JSON 有效' : (validation.error || 'JSON 无效') }}
                    </div>
                    <div class="flex items-center gap-2">
                        <button type="button" @click="formatJson" :disabled="!validation.ok" class="json-editor-action">
                            格式化
                        </button>
                        <button type="button" @click="minifyJson" :disabled="!validation.ok" class="json-editor-action">
                            压缩
                        </button>
                    </div>
                </div>
                <div class="json-editor-surface" :style="{ minHeight: (rows * 22 + 42) + 'px' }">
                    <pre ref="overlay" aria-hidden="true" class="json-editor-overlay" v-html="highlightedHtml"></pre>
                    <textarea
                        ref="input"
                        :value="modelValue"
                        spellcheck="false"
                        autocapitalize="off"
                        autocomplete="off"
                        autocorrect="off"
                        :rows="rows"
                        class="json-editor-input"
                        @input="updateValue"
                        @scroll="syncScroll"
                        @keydown="handleKeydown"
                    ></textarea>
                </div>
            </div>
        `
    },
    StatusSwitch: {
        props: {
            enabled: { type: Boolean, required: true },
            onLabel: { type: String, default: '启用' },
            offLabel: { type: String, default: '停用' },
            showLabels: { type: Boolean, default: false }
        },
        emits: ['toggle'],
        template: `
            <button
                @click="$emit('toggle')"
                :class="showLabels ? 'min-h-[42px] w-full justify-between px-3 py-2' : 'w-auto justify-center px-1 py-1'"
                class="flex items-center gap-3 rounded-3xl bg-slate-900/88 text-left text-sm font-semibold text-slate-100"
            >
                <span v-if="showLabels" class="flex min-w-0 items-center gap-2">
                    <svg viewBox="0 0 24 24" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.8" :class="enabled ? 'text-blue-300' : 'text-slate-400'">
                        <path d="M12 6v12M6 12h12" v-if="enabled"></path>
                        <path d="M6 12h12" v-else></path>
                    </svg>
                    <span class="truncate" :class="enabled ? 'text-blue-300' : 'text-slate-400'">{{ enabled ? onLabel : offLabel }}</span>
                </span>
                <span class="relative h-6 w-11 rounded-full transition" :class="enabled ? 'bg-blue-600' : 'bg-slate-700'">
                    <span class="absolute top-0.5 h-5 w-5 rounded-full bg-white transition" :class="enabled ? 'left-5' : 'left-0.5'"></span>
                </span>
            </button>
        `
    },
    LogStreamPanel: {
        props: {
            logs: { type: Array, required: true },
            title: { type: String, default: '实时日志' },
            subtitle: { type: String, default: '前端最多保留 100 条' },
            compact: { type: Boolean, default: false },
            showHeader: { type: Boolean, default: true }
        },
        methods: {
            ansiToHtml(input) {
                const colorMap = {
                    '31': '#ef4444',
                    '32': '#10b981',
                    '33': '#f59e0b',
                    '34': '#3b82f6',
                    '36': '#06b6d4'
                };
                const text = String(input || '');
                const regex = /\u001b\[(\d+)m/g;
                let lastIndex = 0;
                let activeColor = '';
                let html = '';

                const escapeHtml = value => value
                    .replace(/&/g, '&amp;')
                    .replace(/</g, '&lt;')
                    .replace(/>/g, '&gt;')
                    .replace(/"/g, '&quot;')
                    .replace(/'/g, '&#39;');

                let match;
                while ((match = regex.exec(text)) !== null) {
                    const chunk = text.slice(lastIndex, match.index);
                    if (chunk) {
                        const escaped = escapeHtml(chunk);
                        html += activeColor ? `<span style="color:${activeColor}">${escaped}</span>` : escaped;
                    }
                    activeColor = match[1] === '0' ? '' : (colorMap[match[1]] || '');
                    lastIndex = regex.lastIndex;
                }

                const tail = text.slice(lastIndex);
                if (tail) {
                    const escaped = escapeHtml(tail);
                    html += activeColor ? `<span style="color:${activeColor}">${escaped}</span>` : escaped;
                }
                return html;
            },
            scrollToBottom() {
                const el = this.$refs.scroller;
                if (!el) return;
                el.scrollTop = el.scrollHeight;
            }
        },
        mounted() {
            this.$nextTick(() => this.scrollToBottom());
        },
        updated() {
            this.$nextTick(() => this.scrollToBottom());
        },
        template: `
            <div class="overflow-hidden rounded-[28px] border border-slate-800 bg-slate-950/92">
                <div v-if="showHeader" class="flex items-center justify-between border-b border-slate-800 px-5 py-4">
                    <div>
                        <div class="text-base font-semibold text-slate-100">{{ title }}</div>
                        <div class="mt-1 text-xs text-slate-400">{{ subtitle }}</div>
                    </div>
                    <div class="rounded-full bg-blue-600/15 px-3 py-1 text-xs font-semibold text-blue-200">{{ logs.length }} / 100</div>
                </div>
                <div ref="scroller" :class="compact ? 'max-h-[280px]' : 'max-h-[65vh]'" class="ui-scroll overflow-y-auto px-5 py-4 font-mono text-xs leading-6">
                    <div v-if="!logs.length" class="text-slate-300">等待日志数据...</div>
                    <div v-for="(log, index) in logs" :key="index" class="border-b border-slate-800 py-2 last:border-b-0">
                        <div class="break-all text-slate-200" v-html="ansiToHtml(log.message)"></div>
                    </div>
                </div>
            </div>
        `
    },
    TcpLogPanel: {
        props: {
            logs: { type: Array, required: true }
        },
        methods: {
            scrollToBottom() {
                const el = this.$refs.scroller;
                if (!el) return;
                el.scrollTop = el.scrollHeight;
            }
        },
        mounted() {
            this.$nextTick(() => this.scrollToBottom());
        },
        updated() {
            this.$nextTick(() => this.scrollToBottom());
        },
        template: `
            <div class="overflow-hidden rounded-[28px] border border-slate-800 bg-slate-950/92">
                <div class="flex items-center justify-between border-b border-slate-800 px-5 py-4">
                    <div class="text-base font-semibold text-slate-100">TCP 实时日志</div>
                    <div class="rounded-full bg-blue-600/15 px-3 py-1 text-xs font-semibold text-blue-200">{{ logs.length }} / 100</div>
                </div>
                <div ref="scroller" class="ui-scroll max-h-[60vh] overflow-y-auto px-5 py-4 font-mono text-xs leading-6">
                    <div v-if="!logs.length" class="text-slate-300">等待 TCP 转发日志...</div>
                    <div v-for="(log, index) in logs" :key="index" class="space-y-1 border-b border-slate-800 py-3 last:border-b-0">
                        <div class="flex flex-wrap items-center gap-2 text-slate-200">
                            <span class="rounded-full bg-slate-900 px-2 py-0.5 text-blue-200">{{ log.source || '--' }}</span>
                            <span class="text-slate-400">-></span>
                            <span class="rounded-full bg-slate-900 px-2 py-0.5 text-blue-300">{{ log.target || '--' }}</span>
                        </div>
                        <div class="flex flex-wrap items-center gap-3 text-slate-400">
                            <span>大小 {{ log.bytes || 0 }} B</span>
                            <span>耗时 {{ log.duration_ms || 0 }} ms</span>
                        </div>
                    </div>
                </div>
            </div>
        `
    }
};
})();
