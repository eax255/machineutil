package machineutil

import (
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/coreos/go-systemd/unit"
	"github.com/eax255/systemd-containers/machineutil/util"
	"github.com/godbus/dbus/v5"
)

type Machine struct {
	Name    string
	object  dbus.BusObject
	manager MachineUtil
}

func (m *Machine) Status() (string, error) {
	var result string
	err := m.object.Call("org.freedesktop.DBus.Properties.Get", 0, machinedDbusMachineInterface, "State").Store(&result)
	return result, err
}

func (m *Machine) Running() bool {
	result, err := m.Status()
	if err != nil {
		return false
	}
	return result == "running"
}

func (m *Machine) EnsureOptions(log *slog.Logger, opts []*unit.UnitOption) (bool, error) {
	file_path := "/etc/systemd/nspawn/" + m.Name + ".nspawn"
	return util.EnsureUnit(log, file_path, opts)
}

func (m *Machine) EnsureOverride(log *slog.Logger, opts []*unit.UnitOption) (bool, error) {
	file_path := "/etc/systemd/system/systemd-nspawn@" + m.Name + ".service.d/machineutil.conf"
	return util.EnsureUnit(log, file_path, opts)
}

func (m *Machine) Addresses() ([]netip.Addr, error) {
	var result []struct {
		Version int
		Addr    []byte
	}
	err := m.object.Call(machinedDbusMachineInterface+".GetAddresses", 0).Store(&result)
	if err != nil {
		return nil, err
	}
	retval := make([]netip.Addr, len(result))
	for i, res := range result {
		var ok bool
		retval[i], ok = netip.AddrFromSlice(res.Addr)
		if !ok {
			return nil, fmt.Errorf("Got invalid ip %d %x", res.Version, res.Addr)
		}
	}
	return retval, nil
}

func (m *Machine) WaitForAddress() ([]netip.Addr, error) {
	for {
		addrs, err := m.Addresses()
		if err != nil {
			return nil, err
		}
		var result []netip.Addr
		for _, addr := range addrs {
			switch {
			case !addr.IsValid():
			case addr.IsUnspecified():
			case addr.IsLoopback():
			case addr.IsLinkLocalUnicast():
			case addr.IsLinkLocalMulticast():
			case addr.IsInterfaceLocalMulticast():
			case addr.IsMulticast():
			default:
				result = append(result, addr)
			}
		}
		if len(result) > 0 {
			return result, nil
		}
		time.Sleep(1)
	}
}

func (m *Machine) Start() error {
	if m.Running() {
		return nil
	}
	log := slog.With("machine", m.Name)
	log.Debug("Starting machine job")
	job, err := m.manager.Start("systemd-nspawn@" + m.Name + ".service")
	if err != nil {
		return err
	}
	err = job.Wait()
	if err != nil {
		return err
	}
	log.Debug("Job completed, waiting for unit")
	for {
		result, err := m.Status()
		if err != nil {
			log.Error("Unexpected error", "error", err)
			return err
		}
		if result == "running" {
			break
		}
		time.Sleep(time.Second)
	}
	return nil
}

func (m *Machine) Stop() error {
	if !m.Running() {
		return nil
	}
	job, err := m.manager.Stop("systemd-nspawn@" + m.Name + ".service")
	if err != nil {
		return err
	}
	err = job.Wait()
	if err != nil {
		return err
	}
	for m.Running() {
		time.Sleep(time.Second)
	}
	return nil
}

func (m *Machine) Exists() bool {
	_, err := m.manager.GetImage(m.Name)
	if err != nil {
		return false
	}
	return true
}

func (m *Machine) Remove() error {
	return m.manager.Remove(m.Name)
}
