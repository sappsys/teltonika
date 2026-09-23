package usb

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SecStat is a parsed <SECSTAT>a,b,c,d,e response (Configurator KeywordInfo).
type SecStat struct {
	Raw           string
	ResultSuccess bool // index 0
	IsSecured     bool // index 1 — keyword configured on device
	IsAuthorized  bool // index 2 — this USB session unlocked
	IsLocked      bool // index 3 — retry budget exhausted
	RetryLeft     int  // index 4
}

// NeedsKeywordUnlock reports a secured device that is not yet authorized on this session.
func (s SecStat) NeedsKeywordUnlock() bool {
	return s.IsSecured && !s.IsAuthorized && !s.IsLocked
}

// ParseSecStat extracts the last <SECSTAT>… line from a text reply.
func ParseSecStat(text string) (SecStat, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	var line string
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "<SECSTAT>") {
			line = ln
		}
	}
	if line == "" {
		return SecStat{}, false
	}
	rest := strings.TrimPrefix(line, "<SECSTAT>")
	parts := strings.Split(rest, ",")
	st := SecStat{Raw: line}
	if len(parts) > 0 {
		st.ResultSuccess = parts[0] == "1"
	}
	if len(parts) > 1 {
		st.IsSecured = parts[1] == "1"
	}
	if len(parts) > 2 {
		st.IsAuthorized = parts[2] == "1"
	}
	if len(parts) > 3 {
		st.IsLocked = parts[3] == "1"
	}
	if len(parts) > 4 {
		st.RetryLeft, _ = strconv.Atoi(strings.TrimSpace(parts[4]))
	}
	return st, true
}

// isBootNoiseReply reports AT/CMD.DEBUG chatter instead of a Configurator SECSTAT.
// Post-DFU the CDC often answers :sec_status early while :sec_login still hits the
// AT path ("Command not found! From:3 (AT)") until Configurator finishes bringing up.
func isBootNoiseReply(text string) bool {
	u := strings.ToUpper(text)
	return strings.Contains(u, "CMD.DEBUG") ||
		strings.Contains(u, "COMMAND NOT FOUND") ||
		strings.Contains(u, "FROM:3 (AT)") ||
		strings.Contains(u, "[CMD.")
}

// SecStatus queries :sec_status and parses <SECSTAT>.
func (c *Client) SecStatus() (SecStat, error) {
	raw := c.txRawHard([]byte(":sec_status\r"), 2*time.Second, 300*time.Millisecond, 3500*time.Millisecond)
	text := string(raw)
	st, ok := ParseSecStat(text)
	if !ok {
		return SecStat{}, fmt.Errorf("no SECSTAT in reply: %q", truncate(text, 120))
	}
	return st, nil
}

// ValidateKeyword checks Configurator keyword rules (letters/digits, length ≥ 4).
func ValidateKeyword(keyword string) error {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return fmt.Errorf("empty keyword")
	}
	if len(keyword) < 4 {
		return fmt.Errorf("must be at least 4 characters")
	}
	for _, r := range keyword {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		return fmt.Errorf("must be letters and digits only")
	}
	return nil
}

// SecLogin submits :sec_login:<keyword> and returns the resulting SECSTAT.
// Post-flash boot noise (AT "Command not found") is retried with backoff — that is
// not a burned keyword attempt when no <SECSTAT> comes back.
func (c *Client) SecLogin(keyword string) (SecStat, error) {
	if err := ValidateKeyword(keyword); err != nil {
		return SecStat{}, err
	}
	keyword = strings.TrimSpace(keyword)
	var last string
	const maxAttempts = 8
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if c.isClosed() {
			return SecStat{}, fmt.Errorf("port closed during sec_login")
		}
		if attempt > 1 {
			// Longer pause when the device is still dumping AT/CMD.DEBUG noise.
			backoff := time.Duration(attempt) * 500 * time.Millisecond
			if isBootNoiseReply(last) {
				backoff = time.Duration(attempt) * time.Second
			}
			time.Sleep(backoff)
			c.drain(250 * time.Millisecond)
		}
		c.logf("TX %q (attempt %d)", ":sec_login:***", attempt)
		raw := c.txRawHard([]byte(":sec_login:"+keyword+"\r"), 2500*time.Millisecond, 400*time.Millisecond, 5*time.Second)
		last = string(raw)
		st, ok := ParseSecStat(last)
		if ok {
			return st, nil
		}
		if isBootNoiseReply(last) {
			c.logf("sec_login attempt %d: Configurator not ready yet (%q)", attempt, truncate(last, 80))
			continue
		}
		// Empty / mute — keep trying a few times before giving up.
		if strings.TrimSpace(last) == "" && attempt < maxAttempts {
			continue
		}
	}
	if isBootNoiseReply(last) {
		return SecStat{}, fmt.Errorf("no SECSTAT after sec_login (device still booting / AT noise): %q", truncate(last, 120))
	}
	return SecStat{}, fmt.Errorf("no SECSTAT after sec_login: %q", truncate(last, 120))
}

// EnsureKeywordUnlocked checks :sec_status and, if the session needs a keyword,
// prompts via PromptKeyword (once per call) and sends :sec_login. Wrong guesses burn
// device retry budget — Ctrl-C aborts before sending if the user cancels the prompt.
// Callers may cache a successful keyword across reconnects (e.g. post-DFU reboot).
//
// :sec_status often works before :sec_login; boot-noise replies
// are retried without counting as a wrong keyword.
func (c *Client) EnsureKeywordUnlocked(progress func(string), verbose func(string), prompt func() (string, error)) error {
	var st SecStat
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		st, err = c.SecStatus()
		if err == nil {
			break
		}
		if verbose != nil {
			verbose(fmt.Sprintf("sec_status attempt %d: %s", attempt, err.Error()))
		}
		time.Sleep(time.Duration(attempt) * 400 * time.Millisecond)
		c.drain(150 * time.Millisecond)
	}
	if err != nil {
		// Some firmwares / mid-boot windows answer neither; the caller may retry.
		if verbose != nil {
			verbose("sec_status unavailable; skipping unlock this pass")
		}
		return nil
	}
	if verbose != nil {
		verbose(fmt.Sprintf("SECSTAT secured=%v authorized=%v locked=%v retries=%d",
			st.IsSecured, st.IsAuthorized, st.IsLocked, st.RetryLeft))
	}
	if st.IsLocked {
		return fmt.Errorf("device keyword lock engaged (retry budget exhausted) — unlock via Teltonika Configurator or wait for recovery policy")
	}
	if !st.NeedsKeywordUnlock() {
		if progress != nil && st.IsSecured && st.IsAuthorized {
			progress("Configurator keyword present — session already unlocked")
		}
		return nil
	}
	if progress != nil {
		progress(fmt.Sprintf("Device has a Configurator keyword (session locked; %d tries left)", st.RetryLeft))
	}
	if prompt == nil {
		return fmt.Errorf("Configurator keyword required but no prompt available (run Program_Tracker interactively)")
	}
	kw, err := prompt()
	if err != nil {
		return fmt.Errorf("keyword prompt: %w", err)
	}
	kw = strings.TrimSpace(kw)
	if kw == "" {
		return fmt.Errorf("no keyword entered (Ctrl-C to abort, or re-run and enter the keyword)")
	}

	// Post-DFU: sec_status can succeed while sec_login still returns AT noise.
	// Re-check status and retry login until authorized, locked, or deadline.
	deadline := time.Now().Add(75 * time.Second)
	var lastLoginErr error
	announced := false
	for time.Now().Before(deadline) {
		if c.isClosed() {
			return fmt.Errorf("port closed during keyword unlock")
		}
		st, err = c.SecStatus()
		if err == nil {
			if st.IsLocked {
				return fmt.Errorf("device keyword lock engaged (retry budget exhausted) — unlock via Teltonika Configurator or wait for recovery policy")
			}
			if !st.NeedsKeywordUnlock() {
				if progress != nil && st.IsAuthorized {
					progress("Keyword accepted — session unlocked")
				}
				return nil
			}
		}
		if !announced {
			if progress != nil {
				progress("Unlocking with keyword…")
			}
			announced = true
		}
		st2, loginErr := c.SecLogin(kw)
		if loginErr != nil {
			lastLoginErr = loginErr
			if verbose != nil {
				verbose("sec_login: " + loginErr.Error())
			}
			// Soft failure (boot noise) — wait and try again without treating as wrong keyword.
			if strings.Contains(loginErr.Error(), "still booting") || strings.Contains(loginErr.Error(), "AT noise") {
				if progress != nil {
					progress("Configurator not ready for keyword yet — waiting…")
				}
				time.Sleep(2 * time.Second)
				c.drain(300 * time.Millisecond)
				continue
			}
			return loginErr
		}
		if verbose != nil {
			verbose(fmt.Sprintf("SECSTAT after login secured=%v authorized=%v locked=%v retries=%d",
				st2.IsSecured, st2.IsAuthorized, st2.IsLocked, st2.RetryLeft))
		}
		if st2.IsLocked {
			return fmt.Errorf("keyword rejected — device is now locked (retries exhausted)")
		}
		if !st2.IsAuthorized {
			return fmt.Errorf("keyword rejected (%d tries left)", st2.RetryLeft)
		}
		if progress != nil {
			progress("Keyword accepted — session unlocked")
		}
		return nil
	}
	if lastLoginErr != nil {
		return fmt.Errorf("keyword unlock timed out: %w", lastLoginErr)
	}
	return fmt.Errorf("keyword unlock timed out waiting for Configurator")
}
