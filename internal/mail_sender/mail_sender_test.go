package mail_sender

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFillTemplateIsSinglePass(t *testing.T) {
	out := fillTemplate("<p>{{A}}</p><p>{{B}}</p>", map[string]string{"A": "{{B}}", "B": "b"})
	assert.Equal(t, "<p>{{B}}</p><p>b</p>", out)
}

func TestBuildMessageHeaders(t *testing.T) {
	msg := buildMessage("from@example.test", []string{"to@example.test"}, "Sauté moved\r\nBcc: x@example.test", "<p>body</p>")
	header, body, ok := strings.Cut(msg, "\r\n\r\n")
	assert.True(t, ok)
	assert.Equal(t, "<p>body</p>", body)
	assert.Contains(t, header, "To: to@example.test\r\n")
	assert.Contains(t, header, "Subject: =?utf-8?q?")
	assert.NotContains(t, header, "\r\nBcc:")
}
