package watch

import (
	"strings"
	"testing"
)

func TestPSStringDoublesEmbeddedQuotes(t *testing.T) {
	got := psString(`it's "quoted"`)
	want := `'it''s "quoted"'`
	if got != want {
		t.Errorf("psString = %q, want %q", got, want)
	}
}

func TestXMLEscapeHandlesReservedCharacters(t *testing.T) {
	got := xmlEscape(`<Model> & "friends" it's here`)
	for _, bad := range []string{"<Model>", ` & `, `"friends"`} {
		if containsRaw(got, bad) {
			t.Errorf("xmlEscape(%q) = %q still contains unescaped %q", `<Model> & "friends"`, got, bad)
		}
	}
}

func containsRaw(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestToastScriptCarriesTheAUMIDAndFallsBackToTheBalloon(t *testing.T) {
	note := Notification{Title: "New model", Body: "Something fits your machine now: http://127.0.0.1:27182/models/x"}
	script := toastScript("Itay.LocalLLMAdvisor", note)

	for _, want := range []string{
		"ToastNotificationManager",
		"CreateToastNotifier('Itay.LocalLLMAdvisor')",
		"try {",
		"} catch {",
		"ShowBalloonTip", // the fallback, verbatim balloonScript's content
	} {
		if !containsRaw(script, want) {
			t.Errorf("toastScript is missing %q:\n%s", want, script)
		}
	}
}

func TestToastScriptEscapesTitleAndBodyForBothXMLAndPowerShell(t *testing.T) {
	note := Notification{Title: `Gemma 4 "Nano"`, Body: `fits & runs at 40 tok/s`}
	script := toastScript("aumid", note)
	// The toast's XML text nodes must carry the XML-escaped forms — a raw
	// '"' or '&' there would either break LoadXml or be read as markup.
	// (The balloon fallback later in the same script legitimately repeats
	// the raw title/body — psString only escapes for PowerShell, not XML —
	// so this checks the LoadXml(...) call specifically, not the whole
	// script.)
	loadXML := script[strings.Index(script, "LoadXml("):strings.Index(script, "$toast =")]
	for _, want := range []string{"&#34;Nano&#34;", "fits &amp; runs"} {
		if !containsRaw(loadXML, want) {
			t.Errorf("the toast XML is missing the escaped form %q:\n%s", want, loadXML)
		}
	}
	for _, bad := range []string{`"Nano"`, ` & runs`} {
		if containsRaw(loadXML, bad) {
			t.Errorf("the toast XML contains an unescaped %q:\n%s", bad, loadXML)
		}
	}
}

func TestBalloonScriptSetsTitleAndBody(t *testing.T) {
	script := balloonScript(Notification{Title: "T", Body: "B"})
	for _, want := range []string{"BalloonTipTitle = 'T'", "BalloonTipText = 'B'", "ShowBalloonTip"} {
		if !containsRaw(script, want) {
			t.Errorf("balloonScript is missing %q:\n%s", want, script)
		}
	}
}
