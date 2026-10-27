package handlers

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"recipes/internal/models"
)

func TestValidateMailRequest(t *testing.T) {
	valid := func() models.AdminMailRequest {
		return models.AdminMailRequest{Subject: " New address ", Heading: "We moved", Message: "Hello"}
	}

	req := valid()
	require.NoError(t, validateMailRequest(&req))
	assert.Equal(t, "New address", req.Subject)

	for name, mutate := range map[string]func(*models.AdminMailRequest){
		"missing message":   func(r *models.AdminMailRequest) { r.Message = "  " },
		"label without url": func(r *models.AdminMailRequest) { r.ButtonLabel = "Open" },
		"url without label": func(r *models.AdminMailRequest) { r.ButtonURL = "https://saute.suolumi.fr" },
		"javascript url":    func(r *models.AdminMailRequest) { r.ButtonLabel, r.ButtonURL = "Open", "javascript:alert(1)" },
		"relative url":      func(r *models.AdminMailRequest) { r.ButtonLabel, r.ButtonURL = "Open", "/en/home" },
		"subject too long":  func(r *models.AdminMailRequest) { r.Subject = strings.Repeat("é", maxMailSubjectLen+1) },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid()
			mutate(&r)
			assert.Error(t, validateMailRequest(&r))
		})
	}

	req = valid()
	req.ButtonLabel, req.ButtonURL = "Open", "https://saute.suolumi.fr"
	assert.NoError(t, validateMailRequest(&req))
}

func TestAnnouncementVars(t *testing.T) {
	req := models.AdminMailRequest{
		Subject: "S", Heading: "<b>H</b>",
		Message:     "First line\r\nsecond line\r\n\r\n\r\n<script>x</script>",
		ButtonLabel: "Go", ButtonURL: "https://saute.suolumi.fr/?a=1&b=2",
	}
	vars := announcementVars(req, models.UserDB{Username: "Ann<", Email: "ann@example.test"})

	assert.Equal(t, "&lt;b&gt;H&lt;/b&gt;", vars["HEADING"])
	assert.Equal(t, "Ann&lt;", vars["USER_NAME"])
	assert.Equal(t,
		"<p class=\"description\">First line<br>second line</p>\n<p class=\"description\">&lt;script&gt;x&lt;/script&gt;</p>\n",
		vars["MESSAGE"])
	assert.Contains(t, vars["BUTTON"], `href="https://saute.suolumi.fr/?a=1&amp;b=2"`)

	req.ButtonLabel, req.ButtonURL = "", ""
	assert.Empty(t, announcementVars(req, models.UserDB{})["BUTTON"])
}

func TestMailRecipients(t *testing.T) {
	got := mailRecipients([]models.UserDB{
		{Username: "a", Email: "a@example.test"},
		{Username: "none"},
		{Username: "A again", Email: " A@Example.test"},
		{Username: "b", Email: "b@example.test"},
	})
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Username)
	assert.Equal(t, "b", got[1].Username)
}

func TestMailBroadcaster(t *testing.T) {
	var b mailBroadcaster
	release := make(chan struct{})
	var mu sync.Mutex
	var sentTo []string
	send := func(u models.UserDB) error {
		<-release
		mu.Lock()
		defer mu.Unlock()
		sentTo = append(sentTo, u.Email)
		if u.Email == "bad@example.test" {
			return errors.New("rejected")
		}
		return nil
	}
	users := []models.UserDB{{Email: "a@example.test"}, {Email: "bad@example.test"}, {Email: "c@example.test"}}

	require.True(t, b.start("Subject", users, send))
	assert.False(t, b.start("Other", users, send), "a second broadcast must be refused while one runs")

	s := b.snapshot()
	assert.True(t, s.Running)
	assert.Equal(t, 3, s.Total)
	assert.Equal(t, "Subject", s.Subject)

	close(release)
	require.Eventually(t, func() bool { return !b.snapshot().Running }, time.Second, 5*time.Millisecond)

	s = b.snapshot()
	assert.Equal(t, 2, s.Sent)
	assert.Equal(t, []models.MailBroadcastFailure{{Email: "bad@example.test", Error: "rejected"}}, s.Failed)
	assert.NotNil(t, s.FinishedAt)
	assert.Equal(t, []string{"a@example.test", "bad@example.test", "c@example.test"}, sentTo)

	assert.True(t, b.start("Again", users[:1], func(models.UserDB) error { return nil }))
}
