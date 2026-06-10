# cors

HTTP middleware for Cross-Origin Resource Sharing (CORS). Validates the `Origin` header against an allowlist, sets the appropriate response headers, and handles preflight `OPTIONS` requests.

## Usage

```go
import "github.com/OpenNSW/core/cors"

handler = cors.CORS(&cors.Config{
    AllowedOrigins:   []string{"https://portal.example.com", "https://admin.example.com"},
    AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
    AllowedHeaders:   []string{"Authorization", "Content-Type"},
    AllowCredentials: true,
    MaxAge:           86400, // preflight cache duration in seconds
})(mux)
```

## Behaviour

- Origins not in `AllowedOrigins` receive a `204 No Content` response on preflight with no CORS headers set.
- Preflight `OPTIONS` requests that pass the origin check return `204` with the appropriate `Access-Control-*` headers.
- Actual requests from allowed origins have CORS headers appended to the response.
- `AllowCredentials: true` sets `Access-Control-Allow-Credentials: true`; required when the frontend sends cookies or `Authorization` headers cross-origin.
