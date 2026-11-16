package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestReviewRequiresDeployAnyway(t *testing.T) {
	srv, _ := newAgentServer(t)
	cookie := loginCookie(t, srv)
	spec := `{"name":"web","image":"nginx","privileged":true,"network":"bridge"}`

	rec := call(srv, http.MethodPost, "/api/v1/docker/containers", spec, "", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("create %d %s", rec.Code, rec.Body.String())
	}
	var blocked struct {
		Error    string `json:"error"`
		Findings []struct {
			ID string `json:"id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	if blocked.Findings == nil || blocked.Findings[0].ID != "privileged" {
		t.Fatalf("body %+v", blocked)
	}

	rec = call(srv, http.MethodPost, "/api/v1/docker/review", `{"spec":`+spec+`,"apply":["privileged"]}`, "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("review %d %s", rec.Code, rec.Body.String())
	}
	var reviewed struct {
		Spec struct {
			Privileged bool `json:"privileged"`
		} `json:"spec"`
		Findings []struct {
			ID string `json:"id"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.Spec.Privileged || len(reviewed.Findings) != 0 {
		t.Fatalf("safer spec %+v", reviewed)
	}
}
