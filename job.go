package machineutil

import (
	"time"

	"github.com/godbus/dbus/v5"
)

type Job struct {
	object dbus.BusObject
}

func (j *Job) Wait() error {
	for {
		var state string
		err := j.object.Call("org.freedesktop.DBus.Properties.Get", 0, "org.freedesktop.systemd1.Job", "State").Store(&state)
		if err != nil {
			break
		}
		time.Sleep(time.Second)
	}
	return nil
}
