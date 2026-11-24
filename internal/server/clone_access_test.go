package server

import (
	"net/http"
	"testing"
)

func TestCloneGrantOnlyEnrollsIndependentExecutors(t *testing.T) {
	f, _, _ := reviewFixture(t)
	grant, _ := standaloneRandom("join_")
	const path = "/api/v1/projects/shared"
	admin := standaloneRequest(t, f.h, "POST", path+"/clone-grants", map[string]string{"grant": grant}, f.admin, f.csrf, "", "http://server.test", "1")
	if admin.Code != 200 {
		t.Fatal("admin grant", admin.Code)
	}
	if w := standaloneRequest(t, f.h, "GET", path+"/tasks", nil, nil, "", grant, "", "1"); w.Code != http.StatusUnauthorized {
		t.Fatal("enrollment grant accepted for tasks", w.Code)
	}
	if w := standaloneRequest(t, f.h, "POST", "/api/v1/projects/other/clone-enroll", map[string]string{}, nil, "", grant, "", "1"); w.Code != http.StatusUnauthorized {
		t.Fatal("cross-project enrollment", w.Code)
	}
	users := map[string]bool{}
	for range 2 {
		token, _ := standaloneRandom("ael_")
		enroll := func() int {
			return standaloneRequest(t, f.h, "POST", path+"/clone-enroll", map[string]string{"token": token}, nil, "", grant, "", "1").Code
		}
		if enroll() != 200 || enroll() != 200 {
			t.Fatal("failed enrollment or replay")
		}
		p, err := f.s.token(token)
		if err != nil {
			t.Fatal(err)
		}
		role, err := f.s.role(p, "shared")
		if err != nil || role != "executor" || users[p.UserID] {
			t.Fatalf("invalid executor identity %s %v", role, err)
		}
		users[p.UserID] = true
		if w := standaloneRequest(t, f.h, "POST", path+"/clone-grants", map[string]string{"grant": grant}, nil, "", token, "", "1"); w.Code != http.StatusForbidden {
			t.Fatal("runner issued a new grant", w.Code)
		}
		if err = f.s.revokeToken(p.TokenID); err != nil || enroll() == 200 {
			t.Fatal("replay revived revoked identity")
		}
	}
}
