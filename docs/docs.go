// Package docs serves the embedded OpenAPI contract and Swagger UI.
package docs

import (
	"embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

//go:embed openapi.json swagger-ui/index.html swagger-ui/swagger-initializer.js swagger-ui/swagger-ui.css swagger-ui/swagger-ui-bundle.js
var files embed.FS

// Register makes documentation available in local and container builds without
// a runtime filesystem or external CDN. Only the listed assets are exposed.
func Register(router *gin.Engine) {
	for _, asset := range []struct {
		path, file, contentType string
	}{
		{"/openapi.json", "openapi.json", "application/json; charset=utf-8"},
		{"/swagger/openapi.json", "openapi.json", "application/json; charset=utf-8"},
		{"/swagger/", "swagger-ui/index.html", "text/html; charset=utf-8"},
		{"/swagger/index.html", "swagger-ui/index.html", "text/html; charset=utf-8"},
		{"/swagger/swagger-initializer.js", "swagger-ui/swagger-initializer.js", "application/javascript; charset=utf-8"},
		{"/swagger/swagger-ui.css", "swagger-ui/swagger-ui.css", "text/css; charset=utf-8"},
		{"/swagger/swagger-ui-bundle.js", "swagger-ui/swagger-ui-bundle.js", "application/javascript; charset=utf-8"},
	} {
		body, err := files.ReadFile(asset.file)
		if err != nil {
			panic(err) // All files are required by go:embed at build time.
		}
		router.GET(asset.path, func(c *gin.Context) {
			c.Data(http.StatusOK, asset.contentType, body)
		})
	}
	router.GET("/swagger", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/swagger/index.html")
	})
}
