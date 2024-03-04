package machineutil

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/godbus/dbus/v5"
)

const (
	machinedDbusService          = "org.freedesktop.machine1"
	machinedDbusInterface        = "org.freedesktop.machine1.Manager"
	machinedDbusMachineInterface = "org.freedesktop.machine1.Machine"
	machinedDbusPath             = "/org/freedesktop/machine1"
	systemdDbusService           = "org.freedesktop.systemd1"
	systemdDbusInterface         = "org.freedesktop.systemd1.Manager"
	systemdDbusPath              = "/org/freedesktop/systemd1"
)

var ErrAlreadyExists error = errors.New("image already exist")
var ErrNoSuchImage error = errors.New("image doesn't exist")

type MachineUtil interface {
	ListTemplates(string) (TemplateCollection, error)
	Clone(string, string) (*Machine, error)
	Start(string) (*Job, error)
	Stop(string) (*Job, error)
	Remove(string) error
	GetImage(string) (Image, error)
	GetMachine(string) (*Machine, error)
	DaemonReload() error
}

type machineUtil struct {
	conn      *dbus.Conn
	machined  dbus.BusObject
	systemd   dbus.BusObject
	machines  map[string]*Machine
	templates map[string]*Template
}

func NewMachineUtil() (ret MachineUtil, err error) {
	ret = nil
	c := &machineUtil{
		machines:  make(map[string]*Machine),
		templates: make(map[string]*Template),
	}
	c.conn, err = dbus.SystemBusPrivate()
	if err != nil {
		return
	}
	methods := []dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Getuid()))}
	err = c.conn.Auth(methods)
	if err != nil {
		return
	}
	err = c.conn.Hello()
	if err != nil {
		c.conn.Close()
		return
	}
	c.machined = c.conn.Object(machinedDbusService, machinedDbusPath)
	c.systemd = c.conn.Object(systemdDbusService, systemdDbusPath)
	ret = c
	return
}

func (c *machineUtil) DaemonReload() error {
	return c.systemd.Call(systemdDbusInterface+".Reload", 0).Err
}

func (c *machineUtil) Start(unit string) (*Job, error) {
	var retval dbus.ObjectPath
	err := c.systemd.Call(systemdDbusInterface+".StartUnit", 0, unit, "fail").Store(&retval)
	if err != nil {
		return nil, err
	}
	return &Job{c.conn.Object(systemdDbusService, retval)}, nil
}

func (c *machineUtil) Stop(unit string) (*Job, error) {
	var retval dbus.ObjectPath
	err := c.systemd.Call(systemdDbusInterface+".StopUnit", 0, unit, "fail").Store(&retval)
	if err != nil {
		return nil, err
	}
	return &Job{c.conn.Object(systemdDbusService, retval)}, nil
}

func (c *machineUtil) AddMachine(image Image) (*Machine, error) {
	machine := &Machine{
		Name: image.Name,
		object: c.conn.Object(
			machinedDbusService,
			dbus.ObjectPath(strings.Replace(
				string(image.Path),
				"image",
				"machine",
				1,
			)),
		),
		manager: c,
	}
	c.machines[image.Name] = machine
	return machine, nil
}

func (c *machineUtil) GetMachineFromImage(image Image) (*Machine, error) {
	if res, ok := c.machines[image.Name]; ok {
		return res, nil
	}
	return c.AddMachine(image)
}

func (c *machineUtil) GetMachine(fqdn string) (*Machine, error) {
	image, err := c.GetImage(fqdn)
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "No image") && strings.HasSuffix(msg, "known") {
			return nil, fmt.Errorf("%w: %w", ErrNoSuchImage, err)
		}
		return nil, err
	}
	machine, err := c.GetMachineFromImage(image)
	if err != nil {
		return nil, err
	}
	return machine, nil
}

func (c *machineUtil) GetImage(name string) (retval Image, err error) {
	retval.Name = name
	err = c.machined.Call(machinedDbusInterface+".GetImage", 0, name).Store(&retval.Path)
	return
}

func (c *machineUtil) Clone(src, dst string) (*Machine, error) {
	image, err := c.GetImage(dst)
	if err == nil {
		machine, err := c.GetMachineFromImage(image)
		if err != nil {
			return nil, err
		}
		return machine, ErrAlreadyExists
	}
	call := c.machined.Call(machinedDbusInterface+".CloneImage", 0, src, dst, false)
	if call.Err != nil {
		return nil, call.Err
	}
	return c.GetMachine(dst)
}

func (c *machineUtil) Remove(image string) error {
	if machine, ok := c.machines[image]; ok {
		err := machine.Stop()
		if err != nil {
			return err
		}
	}
	call := c.machined.Call(machinedDbusInterface+".RemoveImage", 0, image)
	if call.Err != nil {
		return call.Err
	}
	delete(c.machines, image)
	delete(c.templates, image)
	return nil
}

type Image struct {
	Name string
	Path dbus.ObjectPath
}

func (c *machineUtil) listImages() ([]Image, error) {
	result := make([][]interface{}, 0)
	if err := c.machined.Call(machinedDbusInterface+".ListImages", 0).Store(&result); err != nil {
		return nil, err
	}
	retval := []Image{}
	for _, i := range result {
		if len(i) < 7 {
			return nil, fmt.Errorf("invalid number of image fields: %s", len(i))
		}
		name, ok := i[0].(string)
		if !ok {
			return nil, fmt.Errorf("failed to typecast image field 0 to string")
		}
		path, ok := i[6].(dbus.ObjectPath)
		if !ok {
			return nil, fmt.Errorf("failed to typecast image field 6 to dbus.ObjectPath")
		}
		retval = append(retval, Image{name, path})
	}
	return retval, nil
}

func (c *machineUtil) ListTemplates(defaultTemplate string) (TemplateCollection, error) {
	images, err := c.listImages()
	if err != nil {
		return nil, err
	}
	retval := make(map[string]TemplateVersions)
	for _, image := range images {
		name, version, found := strings.Cut(image.Name, "-template_")
		if found {
			ver, err := strconv.Atoi(version)
			if err != nil {
				continue
			}
			tmpl, ok := c.templates[image.Name]
			if !ok {
				tmpl = &Template{
					Name:    name,
					Version: ver,
					object:  c.conn.Object(machinedDbusService, image.Path),
					manager: c,
				}
				c.templates[image.Name] = tmpl
			}
			retval[name] = append(retval[name], tmpl)
		}
	}
	for _, imglst := range retval {
		sort.Sort(imglst)
	}
	return &Templates{defaultTemplate, retval}, nil
}
