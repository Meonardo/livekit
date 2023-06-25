package service

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/livekit/livekit-server/pkg/config"
	"github.com/livekit/livekit-server/pkg/routing"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/logger"
)

type CampusService struct {
	router      routing.MessageRouter
	currentNode routing.LocalNode
	config      *config.Config
}

func NewCampusService(
	conf *config.Config,
	router routing.MessageRouter,
	currentNode routing.LocalNode,
) *CampusService {
	s := &CampusService{
		router:      router,
		currentNode: currentNode,
		config:      conf,
	}
	return s
}

func (s *CampusService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var updatedAt time.Time
	if s.currentNode.Stats != nil {
		updatedAt = time.Unix(s.currentNode.Stats.UpdatedAt, 0)
	}
	if time.Since(updatedAt) > 4*time.Second {
		w.WriteHeader(http.StatusNotAcceptable)
		_, _ = w.Write([]byte(fmt.Sprintf("Not Ready\nNode Updated At %s", updatedAt)))
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (s *CampusService) RequestToken(w http.ResponseWriter, r *http.Request) {
	payload, err := ioutil.ReadAll(r.Body)
	if err != nil {
		makeErrorResponse(-1, "No body data found!", w)
		return
	}

	var request joinRoomTokenRequest
	err = json.Unmarshal(payload, &request)
	if err != nil {
		makeErrorResponse(-2, "Decode JSON object failed!", w)
		return
	}

	key := request.ApiKey
	secret := s.config.Keys[key]
	if len(secret) == 0 {
		makeErrorResponse(-11, "Auth key is not available!", w)
		return
	}

	at := auth.NewAccessToken(request.ApiKey, secret)
	grant := &auth.VideoGrant{
		RoomJoin:  true,
		RoomList:  true,
		RoomAdmin: true,
		Room:      request.Room,
	}

	userName := request.Name
	if len(userName) == 0 { // user identity if username is empty
		userName = request.Identity
	}
	at.AddGrant(grant).SetIdentity(request.Identity).SetValidFor(time.Hour).SetName(userName)

	token, err := at.ToJWT()
	if err != nil {
		makeErrorResponse(-12, fmt.Sprintf("Generate token for room: %s failed, %s", request.Room, err), w)
		return
	}

	content := map[string]interface{}{
		"room":   request.Room,
		"apiKey": request.ApiKey,
		"token":  token,
	}
	makeResponse(1, content, w)
}

func makeErrorResponse(code int, msg string, w http.ResponseWriter) {
	logger.Infow(fmt.Sprintf("*****[Response, Failed! Code: (%d), Msg: (%s)]\n", code, msg))

	w.Header().Set("Content-Type", "application/json")
	var resp = map[string]interface{}{
		"code": fmt.Sprint(code), "msg": msg, "data": nil,
	}
	jsonResp, err := json.Marshal(resp)
	if err != nil {
		logger.Errorw("Error happened in JSON marshal. Err: %s", err)
	}
	w.Write(jsonResp)
}

func makeResponse(code int, data map[string]interface{}, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	var resp = map[string]interface{}{
		"code": fmt.Sprint(code), "msg": "", "data": data,
	}
	jsonResp, err := json.Marshal(resp)
	if err != nil {
		logger.Errorw("Error happened in JSON marshal. Err: %s", err)
	}
	logger.Infow(fmt.Sprintf("*****[Response, Success!, Data: %s]\n", string(jsonResp)))

	w.Write(jsonResp)
}

type joinRoomTokenRequest struct {
	ApiKey    string `json:"apiKey"`
	Room      string `json:"room"`
	Identity  string `json:"identity"`
	Name      string `json:"name"`
	ApiSecret string
}
