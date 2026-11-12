package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func adminMessageRouter() *gin.Engine {
	r := gin.New()
	admin := r.Group("/admin", withAuth(1, "admin", "admin"))
	admin.GET("/messages/:id/direction", AdminGetMessageDirection)
	return r
}

func getMessageDirection(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	adminMessageRouter().ServeHTTP(w, jsonReq(http.MethodGet, "/admin/messages/"+id+"/direction", nil))
	return w
}

func seedMessage(t *testing.T, msg models.Message) uint {
	t.Helper()
	sessionID, _ := seedPlayingSession(t)
	msg.SessionID = sessionID
	msg.Role = models.MessageRoleAssistant
	msg.Username = "KP"
	if err := models.DB.Create(&msg).Error; err != nil {
		t.Fatalf("seed message: %v", err)
	}
	return msg.ID
}

func TestAdminGetMessageDirection_ReturnsDirection(t *testing.T) {
	initTestDB(t)
	id := seedMessage(t, models.Message{
		Kind: models.MessageKindWriter, Status: models.MessageStatusDone, WriterDirection: "描述门廊，强调霉味",
		Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{CallID: "call_w1"}},
	})

	w := getMessageDirection(t, itoa(id))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got writerDirectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != id || got.CallID != "call_w1" || got.Auto || got.Direction != "描述门廊，强调霉味" {
		t.Errorf("response = %+v", got)
	}
}

func TestAdminGetMessageDirection_ReportsAutoWriter(t *testing.T) {
	initTestDB(t)
	id := seedMessage(t, models.Message{
		Kind: models.MessageKindWriter, Status: models.MessageStatusDone, WriterDirection: "补写",
		Meta: models.JSONField[*models.MessageMeta]{Data: &models.MessageMeta{Auto: true}},
	})

	w := getMessageDirection(t, itoa(id))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got writerDirectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Auto {
		t.Errorf("auto = false, want true: %+v", got)
	}
}

func TestAdminGetMessageDirection_LegacyWriterHasEmptyDirection(t *testing.T) {
	initTestDB(t)
	id := seedMessage(t, models.Message{Kind: models.MessageKindWriter, Status: models.MessageStatusDone, Content: "旧正文"})

	w := getMessageDirection(t, itoa(id))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var got writerDirectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Direction != "" || got.CallID != "" || got.Auto {
		t.Errorf("response = %+v, want empty direction", got)
	}
}

func TestAdminGetMessageDirection_RejectsNonWriterMessage(t *testing.T) {
	initTestDB(t)
	id := seedMessage(t, models.Message{Kind: models.MessageKindKP, Status: models.MessageStatusDone, Content: "门开了。"})

	if w := getMessageDirection(t, itoa(id)); w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminGetMessageDirection_NotFound(t *testing.T) {
	initTestDB(t)

	if w := getMessageDirection(t, "9999"); w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminGetMessageDirection_InvalidID(t *testing.T) {
	initTestDB(t)

	if w := getMessageDirection(t, "abc"); w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
}
