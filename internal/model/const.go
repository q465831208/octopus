package model

// DefaultUserAgent 出站 HTTP 请求的默认浏览器 User-Agent。
// 众多 OpenAI 兼容中转/上游使用 Cloudflare 并依赖浏览器 UA 放行，
// Go 默认(Go-http-client/1.1)或 axonhub/1.0 会被 403(error 1010) 拦截，
// 故统一使用主流浏览器 UA；渠道自定义 UA(自定义 Header) 优先。
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"