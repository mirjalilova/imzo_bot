package imzo

// Login
type LoginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}
type LoginResp struct {
	Message string `json:"message"`
	Token   string `json:"token"`
}

// Ask
type AskReq struct {
	ChatRoomID string `json:"chat_room_id"`
	Request    string `json:"request"`
}
type AskRespOK struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}
type AskRespErr struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Final
type FinalResp struct {
	Response string `json:"responce"`
}
