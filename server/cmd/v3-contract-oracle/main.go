//go:build v3oracle

// Test-only differential oracle. Never package this in the replacement runtime.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/auth"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	output := os.Getenv("V3_ORACLE_OUTPUT")
	if output == "" {
		panic("V3_ORACLE_OUTPUT is required")
	}
	temporary, err := os.MkdirTemp("", "v3-go-oracle-*")
	must(err)
	defer os.RemoveAll(temporary)
	must(os.WriteFile(filepath.Join(temporary, "server.yaml"), []byte("proto: http\nport:\n  http: 38080\n  https: 38443\njwt:\n  secretKey: v3-test-only-secret\n  revokeTokensOnLogout: true\n"), 0600))
	viper.AddConfigPath(temporary)
	conf := config.GetInstance()
	if conf.JWT.SecretKey != "v3-test-only-secret" {
		panic("oracle must select isolated configuration")
	}
	// Long-lived fixture credentials use a public test-only key, never live data.
	conf.JWT.RefreshTokenDuration = 86400 * 365 * 100
	authn.DefaultStore = authn.NewStore(filepath.Join(temporary, "pwd"))
	if input := os.Getenv("V3_ORACLE_DATABASE"); input != "" {
		data, err := os.ReadFile(input)
		must(err)
		must(os.WriteFile(filepath.Join(temporary, "pwd"), data, 0600))
	} else {
		must(authn.DefaultStore.Create("viewer", "operator-password", authn.RoleUser))
	}
	owner, err := authn.DefaultStore.Get("admin")
	must(err)
	ownerToken, err := middleware.GenerateJWT("admin", owner.TokenVersion)
	must(err)
	viewer, err := authn.DefaultStore.Get("viewer")
	must(err)
	viewerToken, err := middleware.GenerateJWT("viewer", viewer.TokenVersion)
	must(err)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.LimitRequestBody())
	s := auth.NewService()
	r.POST("/api/auth/login", s.Login)
	session := r.Group("/api").Use(middleware.CheckToken())
	session.GET("/auth/account", s.GetAccount)
	session.GET("/auth/password", s.IsPasswordUpdated)
	session.POST("/auth/logout", s.Logout)
	admin := r.Group("/api").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	admin.GET("/auth/users", s.ListUsers)
	admin.POST("/auth/users", s.CreateUser)
	type fixture struct {
		Name     string `json:"name"`
		Method   string `json:"method"`
		Path     string `json:"path"`
		Body     string `json:"body"`
		Token    string `json:"token,omitempty"`
		Status   int    `json:"status"`
		Response any    `json:"response"`
	}
	cases := []fixture{
		{Name: "unauthorized", Method: "GET", Path: "/api/auth/account", Body: "null"},
		{Name: "factory-account", Method: "GET", Path: "/api/auth/account", Body: "null", Token: ownerToken},
		{Name: "factory-password", Method: "GET", Path: "/api/auth/password", Body: "null", Token: ownerToken},
		{Name: "factory-admin-gate", Method: "GET", Path: "/api/auth/users", Body: "null", Token: ownerToken},
		{Name: "viewer-account", Method: "GET", Path: "/api/auth/account", Body: "null", Token: viewerToken},
		{Name: "viewer-forbidden", Method: "GET", Path: "/api/auth/users", Body: "null", Token: viewerToken},
		{Name: "malformed-login", Method: "POST", Path: "/api/auth/login", Body: "{}"},
		{Name: "wrong-login", Method: "POST", Path: "/api/auth/login", Body: `{"username":"viewer","password":"malformed"}`},
	}
	for i := range cases {
		c := &cases[i]
		req := httptest.NewRequest(c.Method, c.Path, bytes.NewBufferString(c.Body))
		req.RemoteAddr = "127.0.0.1:34000"
		req.Header.Set("Content-Type", "application/json")
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		record := httptest.NewRecorder()
		r.ServeHTTP(record, req)
		c.Status = record.Code
		must(json.NewDecoder(strings.NewReader(record.Body.String())).Decode(&c.Response))
	}
	db, err := os.ReadFile(filepath.Join(temporary, "pwd"))
	must(err)
	database := json.RawMessage(db)
	result := map[string]any{"configuration": map[string]any{"secret": "v3-test-only-secret"}, "database": database, "cases": cases, "goVersion": runtime.Version()}
	if input := os.Getenv("V3_ORACLE_RUST_TOKEN_FILE"); input != "" {
		token, err := os.ReadFile(input)
		must(err)
		req := httptest.NewRequest("GET", "/api/auth/account", nil)
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		req.RemoteAddr = "127.0.0.1:34000"
		record := httptest.NewRecorder()
		r.ServeHTTP(record, req)
		if record.Code != 200 {
			panic("Rust token rejected by Go middleware: " + record.Body.String())
		}
		var response struct {
			Data struct {
				Username string `json:"username"`
			} `json:"data"`
		}
		must(json.NewDecoder(strings.NewReader(record.Body.String())).Decode(&response))
		if response.Data.Username != "viewer" {
			panic("Rust token has wrong identity")
		}
		result["rustTokenVerifiedByGo"] = true
	}
	data, err := json.MarshalIndent(result, "", "  ")
	must(err)
	must(os.WriteFile(output, append(data, '\n'), 0600))
	fmt.Println("isolated Go oracle cases:", len(cases))
}
