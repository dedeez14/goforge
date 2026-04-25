package openapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type registerReq struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

type userResp struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Born  time.Time `json:"born,omitempty"`
}

func TestDocumentMarshalsBasicShape(t *testing.T) {
	t.Parallel()
	d := New(Info{Title: "test", Version: "0.1"})
	d.AddOperation(Operation{
		Method: "POST", Path: "/users",
		Summary:      "Create user",
		Tags:         []string{"users"},
		RequestType:  registerReq{},
		ResponseType: userResp{},
		ResponseCode: 201,
		RequiresAuth: false,
	})
	raw, err := d.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v := parsed["openapi"].(string); v != "3.1.0" {
		t.Fatalf("expected openapi 3.1.0, got %s", v)
	}
	body := string(raw)
	for _, want := range []string{`"/users"`, `"post"`, `"email"`, `"format":"uuid"`, `"format":"date-time"`, `"required":["email","password"]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected output to contain %q, got %s", want, body)
		}
	}
}
