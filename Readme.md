# KSMUX

KSMUX is a fast and lightweight HTTP router and web framework for Go, featuring built-in WebSocket support. It is designed to be simple yet powerful, providing a range of features to build modern web applications.

## Features

- **Fast HTTP Routing**: Supports URL parameters and wildcards for flexible routing.
- **WebSocket Support**: Built-in support for WebSocket connections with optional compression.
- **Middleware Support**: Easily add middleware for request handling.
- **Static File Serving**: Serve static files from local or embedded sources.
- **Template Rendering**: Render HTML templates with custom functions.
- **GZIP Compression**: Automatic GZIP compression for responses.
- **Basic Authentication**: Simple basic authentication middleware.
- **CORS Support**: Cross-Origin Resource Sharing (CORS) middleware.
- **Request Logging**: Log incoming requests with customizable logging.
- **Rate Limiting**: Limit the rate of incoming requests.
- **Proxy Support**: Reverse proxy capabilities.
- **Early Hints**: Send early hints to improve performance.
- **Server-Sent Events (SSE)**: Support for server-sent events.

## Installation

To install KSMUX, use the following command:

```bash
go get github.com/kamalshkeir/ksmux@v0.3.8
```

## Basic Usage

Here's a simple example to get started with KSMUX:

```go
package main

import "github.com/kamalshkeir/ksmux"

func main() {
    // Create a new router
    router := ksmux.New()
    
    // Define a route
    router.Get("/", func(c *ksmux.Context) {
        c.Text("Hello World!")
    })
    
    // Start the server
    router.Run(":8080")
}
```

## Routing

KSMUX supports various routing patterns:

```go
// Basic routes
router.Get("/users", handleUsers)
router.Post("/users", createUser)
router.Put("/users/:id", updateUser)
router.Delete("/users/:id", deleteUser)

// URL parameters
router.Get("/users/:id", func(c *ksmux.Context) {
    id := c.Param("id")
    c.Json(map[string]string{"id": id})
})

// Wildcards
router.Get("/files/*filepath", serveFiles)
```

## Context Methods

The `Context` object provides many useful methods for handling requests and responses:

```go
// Response methods
c.Text("Hello")                    // Send plain text
c.Json(data)                       // Send JSON
c.JsonIndent(data)                 // Send indented JSON
c.Html("template.html", data)      // Render HTML template
c.Stream("message")                // Server-sent events
c.Download(bytes, "file.txt")      // Force download
c.Redirect("/new-path")            // HTTP redirect

// Request data
c.Param("id")                      // URL parameter
c.QueryParam("q")                  // Query parameter
c.BodyJson()                       // Parse JSON body
c.BodyStruct(&data)                // Parse body into struct
c.GetCookie("session")             // Get cookie value
c.SetCookie("session", "value")    // Set cookie

// Headers
c.SetHeader("X-Custom", "value")
c.AddHeader("X-Custom", "value")
c.SetStatus(200)

// Files
c.SaveFile(fileHeader, "path")     // Save uploaded file
c.ServeFile("image/png", "path")   // Serve local file
```

## Middleware

Add middleware globally or to specific routes:

```go
// Global middleware
router.Use(ksmux.Logs())
router.Use(ksmux.Gzip())
router.Use(ksmux.Cors())

// Route-specific middleware
router.Get("/admin", adminOnly(handleAdmin))

func adminOnly(next ksmux.Handler) ksmux.Handler {
    return func(c *ksmux.Context) {
        if !isAdmin(c) {
            c.Status(403).Text("Forbidden")
            return
        }
        next(c)
    }
}
```

## WebSocket Support

KSMUX provides built-in support for WebSocket connections:

```go
router.Get("/ws", func(c *ksmux.Context) {
    // Upgrade HTTP connection to WebSocket
    conn, err := c.UpgradeConnection()
    if err != nil {
        return
    }
    
    // Handle WebSocket messages
    for {
        messageType, p, err := conn.ReadMessage()
        if err != nil {
            return
        }
        
        // Echo the message back
        err = conn.WriteMessage(messageType, p)
        if err != nil {
            return
        }
    }
})
```

## Templates

Render HTML templates with custom functions:

```go
// Load templates
router.LocalTemplates("templates/")
// or
router.EmbededTemplates(embededFS, "templates/")

// Add custom template functions
router.NewTemplateFunc("upper", strings.ToUpper)

// Render template
router.Get("/", func(c *ksmux.Context) {
    c.Html("index.html", map[string]any{
        "title": "Home",
        "user": user,
    })
})
```

## Static Files

Serve static files from local or embedded sources:

```go
// Serve local directory
router.LocalStatics("static/", "/static")

// Serve embedded files
router.EmbededStatics(embededFS, "static/", "/static")
```

## Configuration

Configure server settings and cookies:

```go
// Server timeouts
ksmux.READ_TIMEOUT = 10 * time.Second  
ksmux.WRITE_TIMEOUT = 10 * time.Second
ksmux.IDLE_TIMEOUT = 30 * time.Second

// Cookie settings
ksmux.COOKIES_HttpOnly = true
ksmux.COOKIES_SECURE = true
ksmux.COOKIES_SameSite = http.SameSiteStrictMode
ksmux.COOKIES_Expires = 24 * time.Hour
```

## License

BSD 3-Clause License. See [LICENSE](LICENSE) for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## Author

Kamal SHKEIR

## Support

If you find this project helpful, please give it a ⭐️