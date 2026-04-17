module.exports = {
    darkMode: 'class',
    content: [
        './internal/admin/static/index.html',
        './internal/admin/static/*.js'
    ],
    theme: {
        extend: {
            fontFamily: {
                sans: ['Inter', 'system-ui', 'sans-serif']
            },
            borderRadius: {
                '4xl': '2rem'
            },
            boxShadow: {
                glass: '0 24px 80px rgba(2, 6, 23, 0.42)',
                glow: '0 0 0 1px rgba(37, 99, 235, 0.2), 0 14px 44px rgba(37, 99, 235, 0.22)'
            },
            colors: {
                brand: {
                    500: '#2563eb',
                    600: '#1d4ed8'
                }
            }
        }
    }
};
