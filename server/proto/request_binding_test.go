package proto

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Releases disable Gin's MessagePack codec; the API's existing JSON, form and
// query binding must keep validation and explicit false values intact.
func TestHTTPBindingFormats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ method, contentType, target, body string }{
		{"POST", "application/json", "/", `{"name":"test","enabled":false}`},
		{"POST", "application/x-www-form-urlencoded", "/", "name=test&enabled=false"},
		{"GET", "", "/?name=test&enabled=false", ""},
	} {
		t.Run(tc.method+tc.contentType, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			var req struct {
				Name    string `json:"name" form:"name" validate:"required"`
				Enabled *bool  `json:"enabled" form:"enabled" validate:"required"`
			}
			// Gin binds the query string of a GET request as form values.
			err := ParseFormRequest(c, &req)
			if err != nil || req.Name != "test" || req.Enabled == nil || *req.Enabled {
				t.Fatalf("%+v: %v", req, err)
			}
		})
	}
}
