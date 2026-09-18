package handler

import (
	"UnifiedInfraTopology/internal/handler"
	"UnifiedInfraTopology/internal/middleware"
	jwt2 "UnifiedInfraTopology/pkg/jwt"
	"UnifiedInfraTopology/pkg/log"
	"bytes"
	"fmt"
	"github.com/gavv/httpexpect/v2"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

var (
	userId = "xxx"
)
var logger *log.Logger
var hdl *handler.Handler
var jwt *jwt2.JWT
var router *gin.Engine

func TestMain(m *testing.M) {
	fmt.Println("begin")
	// 测试独立配置，不读取开发环境的真实凭据。
	conf := viper.New()
	conf.Set("log.mode", "console")
	conf.Set("security.jwt.key", "user-handler-test-only-key")

	logger = log.NewLog(conf)
	hdl = handler.NewHandler(logger)

	jwt = jwt2.NewJwt(conf)
	gin.SetMode(gin.TestMode)
	router = gin.Default()
	router.Use(
		middleware.CORSMiddleware(),
		middleware.ResponseLogMiddleware(logger),
		middleware.RequestLogMiddleware(logger),
		//middleware.SignMiddleware(log),
	)

	code := m.Run()
	fmt.Println("test end")

	os.Exit(code)
}

func performRequest(r http.Handler, method, path string, body *bytes.Buffer) *httptest.ResponseRecorder {
	req, _ := http.NewRequest(method, path, body)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

func genToken(t *testing.T) string {
	token, err := jwt.GenToken(userId, time.Now().Add(time.Hour*24*90))
	if err != nil {
		t.Error(err)
		return token
	}
	return token
}

func newHttpExcept(t *testing.T, router *gin.Engine) *httpexpect.Expect {
	return httpexpect.WithConfig(httpexpect.Config{
		Client: &http.Client{
			Transport: httpexpect.NewBinder(router),
			Jar:       httpexpect.NewCookieJar(),
		},
		Reporter: httpexpect.NewAssertReporter(t),
		Printers: []httpexpect.Printer{
			// httpexpect.NewDebugPrinter(t, true),
		},
	})
}
