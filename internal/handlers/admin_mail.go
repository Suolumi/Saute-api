package handlers

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"

	"recipes/internal/jwt_manager"
	"recipes/internal/models"
	"recipes/internal/utils"
)

const (
	maxMailSubjectLen = 200
	maxMailHeadingLen = 200
	maxMailMessageLen = 20000
)

var paragraphBreak = regexp.MustCompile(`\n\s*\n`)

// validateMailRequest trims req in place and checks it is sendable.
func validateMailRequest(req *models.AdminMailRequest) error {
	req.Subject = strings.TrimSpace(req.Subject)
	req.Heading = strings.TrimSpace(req.Heading)
	req.Message = strings.TrimSpace(req.Message)
	req.ButtonLabel = strings.TrimSpace(req.ButtonLabel)
	req.ButtonURL = strings.TrimSpace(req.ButtonURL)

	switch {
	case req.Subject == "" || req.Heading == "" || req.Message == "":
		return errors.New("subject, heading and message are required")
	case utf8.RuneCountInString(req.Subject) > maxMailSubjectLen:
		return fmt.Errorf("subject must be at most %d characters", maxMailSubjectLen)
	case utf8.RuneCountInString(req.Heading) > maxMailHeadingLen:
		return fmt.Errorf("heading must be at most %d characters", maxMailHeadingLen)
	case utf8.RuneCountInString(req.Message) > maxMailMessageLen:
		return fmt.Errorf("message must be at most %d characters", maxMailMessageLen)
	case (req.ButtonLabel == "") != (req.ButtonURL == ""):
		return errors.New("button_label and button_url must be set together")
	}
	if req.ButtonURL != "" {
		u, err := url.Parse(req.ButtonURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("button_url must be an absolute http(s) URL")
		}
	}
	return nil
}

// announcementVars fills the announcement template for one recipient. Every
// value is HTML-escaped; the message's blank lines start new paragraphs.
func announcementVars(req models.AdminMailRequest, user models.UserDB) map[string]string {
	var message strings.Builder
	for _, p := range paragraphBreak.Split(strings.ReplaceAll(req.Message, "\r\n", "\n"), -1) {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		escaped := strings.ReplaceAll(html.EscapeString(p), "\n", "<br>")
		message.WriteString(`<p class="description">` + escaped + "</p>\n")
	}

	button := ""
	if req.ButtonURL != "" {
		button = fmt.Sprintf(`<div class="button-container"><a href="%s" class="cta-button">%s</a></div>`,
			html.EscapeString(req.ButtonURL), html.EscapeString(req.ButtonLabel))
	}

	return map[string]string{
		"SUBJECT":    html.EscapeString(req.Subject),
		"HEADING":    html.EscapeString(req.Heading),
		"MESSAGE":    message.String(),
		"BUTTON":     button,
		"USER_NAME":  html.EscapeString(user.Username),
		"USER_EMAIL": html.EscapeString(user.Email),
	}
}

// mailRecipients drops users without an email and repeated addresses.
func mailRecipients(users []models.UserDB) []models.UserDB {
	seen := make(map[string]bool, len(users))
	out := make([]models.UserDB, 0, len(users))
	for _, u := range users {
		key := strings.ToLower(strings.TrimSpace(u.Email))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, u)
	}
	return out
}

// mailBroadcaster runs at most one broadcast at a time and keeps the status
// of the last one in memory (lost on restart, as is a broadcast in flight).
type mailBroadcaster struct {
	mu     sync.Mutex
	status models.MailBroadcastStatus
}

// start begins sending to recipients in the background. It returns false if
// a broadcast is already running.
func (b *mailBroadcaster) start(subject string, recipients []models.UserDB, send func(models.UserDB) error) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.status.Running {
		return false
	}
	now := time.Now()
	b.status = models.MailBroadcastStatus{
		Running:   true,
		Subject:   subject,
		Total:     len(recipients),
		Failed:    []models.MailBroadcastFailure{},
		StartedAt: &now,
	}
	go b.run(recipients, send)
	return true
}

func (b *mailBroadcaster) run(recipients []models.UserDB, send func(models.UserDB) error) {
	for _, u := range recipients {
		err := send(u)
		b.mu.Lock()
		if err != nil {
			utils.LogError("Could not send broadcast mail", err, "email", u.Email)
			b.status.Failed = append(b.status.Failed, models.MailBroadcastFailure{Email: u.Email, Error: err.Error()})
		} else {
			b.status.Sent++
		}
		b.mu.Unlock()
	}
	b.mu.Lock()
	now := time.Now()
	b.status.Running = false
	b.status.FinishedAt = &now
	b.mu.Unlock()
}

func (b *mailBroadcaster) snapshot() models.MailBroadcastStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.status
	s.Failed = append([]models.MailBroadcastFailure{}, b.status.Failed...)
	return s
}

func (h *Handlers) bindMailRequest(c echo.Context) (models.AdminMailRequest, error) {
	var req models.AdminMailRequest
	if err := c.Bind(&req); err != nil {
		return req, err
	}
	return req, validateMailRequest(&req)
}

func (h *Handlers) sendAnnouncement(req models.AdminMailRequest, user models.UserDB) error {
	return h.ms.SendMail(req.Subject, []string{user.Email}, h.ms.AnnouncementMail, announcementVars(req, user))
}

// AdminPreviewMail renders an announcement as the calling admin would get it.
func (h *Handlers) AdminPreviewMail(c echo.Context) error {
	req, err := h.bindMailRequest(c)
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), err, c)
	}
	caller, err := h.db.GetUserById(jwt_manager.GetJwt[*models.TokenClaims](c).UserId)
	if err != nil {
		return errorResponse(http.StatusNotFound, "User not found", err, c)
	}
	rendered, err := h.ms.Render(h.ms.AnnouncementMail, announcementVars(req, caller))
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not render the mail", err, c)
	}
	return c.JSON(http.StatusOK, models.AdminMailPreview{HTML: rendered})
}

// AdminSendMail sends an announcement to the calling admin (test_only), or
// starts sending it to every user in the background.
func (h *Handlers) AdminSendMail(c echo.Context) error {
	req, err := h.bindMailRequest(c)
	if err != nil {
		return errorResponse(http.StatusBadRequest, err.Error(), err, c)
	}

	if req.TestOnly {
		caller, err := h.db.GetUserById(jwt_manager.GetJwt[*models.TokenClaims](c).UserId)
		if err != nil {
			return errorResponse(http.StatusNotFound, "User not found", err, c)
		}
		if err := h.sendAnnouncement(req, caller); err != nil {
			return errorResponse(http.StatusInternalServerError, "Could not send the test mail", err, c)
		}
		return messageResponse(http.StatusOK, "Test mail sent to "+caller.Email, c)
	}

	users, _, err := h.db.GetUsers("", 0, 0)
	if err != nil {
		return errorResponse(http.StatusInternalServerError, "Could not get users", err, c)
	}
	recipients := mailRecipients(users)
	if !h.broadcast.start(req.Subject, recipients, func(u models.UserDB) error { return h.sendAnnouncement(req, u) }) {
		return errorResponse(http.StatusConflict, "A broadcast is already running", nil, c)
	}
	return c.JSON(http.StatusAccepted, h.broadcast.snapshot())
}

// AdminMailBroadcastStatus reports the running broadcast, or the last one.
func (h *Handlers) AdminMailBroadcastStatus(c echo.Context) error {
	return c.JSON(http.StatusOK, h.broadcast.snapshot())
}
