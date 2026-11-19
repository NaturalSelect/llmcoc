package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/llmcoc/server/internal/models"
)

func sessionTableRouter(userID uint, username, role string) *gin.Engine {
	r := gin.New()
	auth := withAuth(userID, username, role)
	r.GET("/sessions/:id/ooc", auth, GetOOCMessages)
	r.POST("/sessions/:id/ooc", auth, PostOOCMessage)
	return r
}

func TestOOCMessages_CRUDPermissionsAndBroadcast(t *testing.T) {
	initTestDB(t)
	resetHubs(t)
	host := seedUser(t, "host", "user", 0, 3)
	p2 := seedUser(t, "p2", "user", 0, 3)
	stranger := seedUser(t, "stranger", "user", 0, 3)
	sid := seedScenario(t, "S")
	c1 := seedCard(t, host, "甲")
	c2 := seedCard(t, p2, "乙")

	sess := models.GameSession{
		Name: "OOC Room", ScenarioID: sid, Status: models.SessionStatusPlaying,
		MaxPlayers: 4, CreatedBy: host, HasPassword: true,
	}
	models.DB.Create(&sess)
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: host, CharacterCardID: c1})
	models.DB.Create(&models.SessionPlayer{SessionID: sess.ID, UserID: p2, CharacterCardID: c2})

	sub, _, _ := hubFor(sess.ID).SubscribeFor(0, host, false)
	defer hubFor(sess.ID).Unsubscribe(sub)

	// 参与者发言成功并附带角色名、广播 ooc_message
	rP2 := sessionTableRouter(p2, "p2", "user")
	w := httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": " 等下我们先查图书馆？ "}))
	if w.Code != http.StatusCreated {
		t.Fatalf("post ooc want 201, got %d body=%s", w.Code, w.Body.String())
	}
	var created models.SessionOOCMessage
	json.NewDecoder(w.Body).Decode(&created)
	if created.CharacterName != "乙" || created.Content != "等下我们先查图书馆？" {
		t.Fatalf("unexpected created msg: %+v", created)
	}
	ev := recvEvent(t, sub)
	if ev.Type != "ooc_message" {
		t.Fatalf("expected ooc_message event, got %+v", ev)
	}

	// 空内容或超长内容被拒
	w = httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": "   "}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty ooc want 400, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rP2.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": strings.Repeat("啊", 501)}))
	if w.Code != http.StatusBadRequest {
		t.Errorf("overlong ooc want 400, got %d", w.Code)
	}

	// 非成员在加密房间既不能发也不能看
	rStranger := sessionTableRouter(stranger, "stranger", "user")
	w = httptest.NewRecorder()
	rStranger.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": "插话"}))
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger post want 403, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rStranger.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/ooc", sess.ID), nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("stranger get passworded room want 403, got %d", w.Code)
	}

	// 房主拉取历史，按时间正序返回
	rHost := sessionTableRouter(host, "host", "user")
	w = httptest.NewRecorder()
	rHost.ServeHTTP(w, jsonReq("POST", fmt.Sprintf("/sessions/%d/ooc", sess.ID), map[string]any{"content": "好主意"}))
	if w.Code != http.StatusCreated {
		t.Fatalf("host post want 201, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	rHost.ServeHTTP(w, jsonReq("GET", fmt.Sprintf("/sessions/%d/ooc", sess.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("get ooc want 200, got %d", w.Code)
	}
	var list []models.SessionOOCMessage
	json.NewDecoder(w.Body).Decode(&list)
	if len(list) != 2 || list[0].Content != "等下我们先查图书馆？" || list[1].Content != "好主意" {
		t.Fatalf("unexpected ooc history: %+v", list)
	}
}
