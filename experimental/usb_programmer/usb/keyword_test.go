package usb

import "testing"

func TestParseSecStat(t *testing.T) {
	st, ok := ParseSecStat("<SECSTAT>1,1,0,0,5\r")
	if !ok {
		t.Fatal("expected parse")
	}
	if !st.ResultSuccess || !st.IsSecured || st.IsAuthorized || st.IsLocked || st.RetryLeft != 5 {
		t.Fatalf("%+v", st)
	}
	if !st.NeedsKeywordUnlock() {
		t.Fatal("expected NeedsKeywordUnlock")
	}

	auth, ok := ParseSecStat("noise\n<SECSTAT>1,1,1,0,5\n")
	if !ok || !auth.IsAuthorized || auth.NeedsKeywordUnlock() {
		t.Fatalf("%+v", auth)
	}

	locked, ok := ParseSecStat("<SECSTAT>1,1,0,1,0")
	if !ok || !locked.IsLocked || locked.NeedsKeywordUnlock() {
		t.Fatalf("%+v", locked)
	}

	if _, ok := ParseSecStat("no secstat here"); ok {
		t.Fatal("expected miss")
	}
}

func TestValidateKeyword(t *testing.T) {
	if err := ValidateKeyword("7232atg"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateKeyword("ab"); err == nil {
		t.Fatal("expected short keyword to fail")
	}
	if err := ValidateKeyword("bad-key"); err == nil {
		t.Fatal("expected punctuation to fail")
	}
}

func TestIsBootNoiseReply(t *testing.T) {
	noise := "[2004.01.01 13:36:56]-[CMD.DEBUG]\tCommand not found! From:3 (AT)\\r"
	if !isBootNoiseReply(noise) {
		t.Fatal("expected boot noise")
	}
	if isBootNoiseReply("<SECSTAT>1,1,0,0,5") {
		t.Fatal("SECSTAT is not boot noise")
	}
}
