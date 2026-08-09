package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// The T-V4 gate found a share token written to the request log in full, on
// every page view. Every other route in this system carries its credential in
// a header; `GET /share/:token` is the first where the path *is* the
// credential, so a log file became a set of working links.
func TestAShareTokenIsNotWrittenToTheRequestLog(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var buf strings.Builder
	prevOut, prevFmt := logrus.StandardLogger().Out, logrus.StandardLogger().Formatter
	logrus.SetOutput(&buf)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	t.Cleanup(func() {
		logrus.SetOutput(prevOut)
		logrus.SetFormatter(prevFmt)
	})

	r := gin.New()
	r.Use(RequestLogging())
	r.GET("/share/:token", func(c *gin.Context) { c.Status(http.StatusOK) })
	// A second route, to prove the redaction is not a blanket one: an ordinary
	// path with an id in it is still logged concretely, because that is what an
	// operator greps for.
	r.GET("/api/documents/:id", func(c *gin.Context) { c.Status(http.StatusOK) })

	const secret = "MkTHcTZjVz04Yd67bCR5ZURVO-ylYnnGL1qpELH_1Bo"
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/share/"+secret, nil))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/documents/doc-42", nil))

	out := buf.String()
	if strings.Contains(out, secret) {
		t.Errorf("the share token is in the log; read access to it is the ability to replay the link:\n%s", out)
	}
	if !strings.Contains(out, "/share/:token") {
		t.Errorf("the line does not say which route was hit:\n%s", out)
	}
	if !strings.Contains(out, "/api/documents/doc-42") {
		t.Errorf("an ordinary path lost its id; the redaction is too wide:\n%s", out)
	}
}
