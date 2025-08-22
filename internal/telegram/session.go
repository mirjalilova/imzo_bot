package telegram

type userState int

const (
	stateIdle userState = iota
	stateAwaitLogin
	stateAwaitPassword
	stateReady
)

type session struct {
	State      userState
	LoginCache string
	Token      string
}
