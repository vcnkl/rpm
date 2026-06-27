package envtui

import "strconv"

const (
	zoneRestartAll = "rpm/restart-all"
	zoneDeps       = "rpm/deps"
	zoneFocus      = "rpm/focus"
	zoneFilter     = "rpm/filter"
	zoneQuit       = "rpm/quit"
	zoneLog        = "rpm/log"
	zoneConfirm    = "rpm/confirm"
	zoneCancel     = "rpm/cancel"
)

func zoneRow(prefix string, index int) string {
	return prefix + "row/" + strconv.Itoa(index)
}

func zoneRestart(prefix string, index int) string {
	return prefix + "restart/" + strconv.Itoa(index)
}
