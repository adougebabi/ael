package core

import "testing"

func TestRemotePlainTextAllowsMarkdownCommentsWithoutAcceptingActiveFormats(t *testing.T) {
	for _, text := range []string{"<!-- AEL:BEGIN -->\nAEL project instructions\n<!-- AEL:END -->", "<!-- comment -->\n# Markdown rules", "<!-- comment only -->"} {
		if err := ValidateRemoteArtifactBytes([]byte(text), "text/plain"); err != nil {
			t.Fatalf("markdown comment rejected %v", err)
		}
	}
	for _, text := range []string{"<!-- comment --><html><body>active</body></html>", "<!-- comment --><svg xmlns='http://www.w3.org/2000/svg'/>", "<!-- comment --><?xml version='1.0'?><document/>", "text\x00binary"} {
		if err := ValidateRemoteArtifactBytes([]byte(text), "text/plain"); err == nil {
			t.Fatal("active format or binary accepted as plain text")
		}
	}
}
