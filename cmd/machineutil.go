package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path"

	"github.com/coreos/go-systemd/unit"
	"github.com/eax255/systemd-containers/machineutil"
	"github.com/eax255/systemd-containers/machineutil/util"
	"gopkg.in/yaml.v3"
)

type MountPoint struct {
	Name         string
	Device       string
	Target       string
	MountPoint   string
	FS           string
	AutoFs       bool
	Options      string
	MountOptions []*unit.UnitOption
}

func (m *MountPoint) Normalize() {
	if m.MountPoint == "" {
		m.MountPoint = "/var/lib/machines/" + m.Name
	}
	if m.FS != "" {
		m.MountOptions = append(m.MountOptions, &unit.UnitOption{
			Section: "Mount",
			Name:    "Type",
			Value:   m.FS,
		})
	}
	if m.AutoFs {
		if m.Options != "" {
			m.Options += ",x-systemd.makefs,x-systemd.growfs"
		} else {
			m.Options = "x-systemd.makefs,x-systemd.growfs"
		}
	}
	if m.Options != "" {
		found := false
		for _, mnt := range m.MountOptions {
			if mnt.Section != "Mount" {
				continue
			}
			if mnt.Name != "Options" {
				continue
			}
			found = true
			mnt.Value += "," + m.Options
			break
		}
		if !found {
			m.MountOptions = append(m.MountOptions, &unit.UnitOption{
				Section: "Mount",
				Name:    "Options",
				Value:   m.Options,
			})
		}
	}
}

func (m *MountPoint) GetNspawn() []*unit.UnitOption {
	return []*unit.UnitOption{
		&unit.UnitOption{
			Section: "Files",
			Name:    "Bind",
			Value:   m.MountPoint + ":" + m.Target + ":idmap",
		},
	}
}

func (m *MountPoint) Unit() string {
	return unit.UnitNamePathEscape(m.MountPoint) + ".mount"
}

func (m *MountPoint) CreateMount(log *slog.Logger) (bool, error) {
	opts := []*unit.UnitOption{
		&unit.UnitOption{
			Section: "Unit",
			Name:    "Description",
			Value:   "Machineutil mountpoint " + m.Name,
		},
		&unit.UnitOption{
			Section: "Unit",
			Name:    "After",
			Value:   "blockdev@" + unit.UnitNamePathEscape(m.Device),
		},
		&unit.UnitOption{
			Section: "Mount",
			Name:    "What",
			Value:   m.Device,
		},
		&unit.UnitOption{
			Section: "Mount",
			Name:    "Where",
			Value:   m.MountPoint,
		},
	}
	mount_unit := "/etc/systemd/system/" + m.Unit()
	opts = append(opts, m.MountOptions...)
	return util.EnsureUnit(log, mount_unit, opts)
}

func (m *MountPoint) RemoveMount(log *slog.Logger) (bool, error) {
	opts := []*unit.UnitOption{}
	mount_unit := "/etc/systemd/system/" + m.Unit()
	return util.EnsureUnit(log, mount_unit, opts)
}

func (m *MountPoint) GetOverride() []*unit.UnitOption {
	return []*unit.UnitOption{
		&unit.UnitOption{
			Section: "Unit",
			Name:    "RequiresMountsFor",
			Value:   m.MountPoint,
		},
	}
}

type CommandDescription struct {
	Command           []string
	WrapperParameters []string
	AppendFqdn        bool
	AppendAddr        bool
	Local             bool
	Stdin             string
	StdinFile         string
	StdoutFile        string
	StdoutAppend      bool
	StderrFile        string
	StderrAppend      bool
	Mode              os.FileMode
}

func (cmd *CommandDescription) Run(fqdn string, addrs []netip.Addr) (err error) {
	if cmd.Mode == 0 {
		cmd.Mode = 0600
	}
	args := []string{}
	var wrapper *exec.Cmd
	if !cmd.Local {
		args = append(args, "systemd-run", "-M", fqdn, "-P")
		args = append(args, cmd.WrapperParameters...)
		args = append(args, "--")
		args = append(args, cmd.Command...)
	} else {
		args = append(args, cmd.Command...)
	}
	if cmd.AppendFqdn {
		args = append(args, fqdn)
	}
	if cmd.AppendAddr {
		for _, addr := range addrs {
			args = append(args, addr.String())
		}
	}
	slog.Debug("Running command", "command", args)
	wrapper = exec.Command(args[0], args[1:]...)
	var stdin *os.File
	var stdout *os.File
	var stderr *os.File
	defer func() {
		if stdin != nil {
			stdin.Close()
		}
		if stdout != nil {
			stdout.Close()
		}
		if stderr != nil {
			stderr.Close()
		}
	}()
	if cmd.StdinFile != "" {
		slog.Debug("Using stdin", "file", cmd.StdinFile)
		stdin, err = os.Open(cmd.StdinFile)
		if err != nil {
			return
		}
		wrapper.Stdin = stdin
	} else if cmd.Stdin != "" {
		slog.Debug("Using stdin", "static", cmd.Stdin)
		wrapper.Stdin = bytes.NewReader([]byte(cmd.Stdin))
	}
	if cmd.StdoutFile != "" {
		slog.Debug("Using stdout", "file", cmd.StdoutFile, "append", cmd.StdoutAppend)
		if cmd.StdoutAppend {
			stdout, err = os.OpenFile(cmd.StdoutFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, cmd.Mode)
		} else {
			stdout, err = os.OpenFile(cmd.StdoutFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cmd.Mode)
		}
		if err != nil {
			return
		}
		wrapper.Stdout = stdout
	}
	if cmd.StderrFile != "" {
		slog.Debug("Using stderr", "file", cmd.StderrFile, "append", cmd.StderrAppend)
		if cmd.StderrAppend {
			stderr, err = os.OpenFile(cmd.StderrFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, cmd.Mode)
		} else {
			stderr, err = os.OpenFile(cmd.StderrFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cmd.Mode)
		}
		if err != nil {
			return
		}
		wrapper.Stderr = stderr
	}
	err = wrapper.Run()
	return
}

type Machine struct {
	Template     string
	Fqdn         string
	Options      []*unit.UnitOption
	Overrides    []*unit.UnitOption
	Mounts       []*MountPoint
	Creation     []*CommandDescription
	CreationPost []*CommandDescription
	Startup      []*CommandDescription
	CommandsPre  []*CommandDescription
	Commands     []*CommandDescription
	runCreation  bool
	runStartup   bool
}

func (m *Machine) Normalize() error {
	for _, mnt := range m.Mounts {
		mnt.Normalize()
		m.Options = append(m.Options, mnt.GetNspawn()...)
		m.Overrides = append(m.Overrides, mnt.GetOverride()...)
	}
	return nil
}

func (m *Machine) EnsureMounts(log *slog.Logger) (changed bool, err error) {
	changed = false
	var c bool
	for _, mnt := range m.Mounts {
		c, err = mnt.CreateMount(log)
		if err != nil {
			return
		}
		if c {
			changed = true
		}
	}
	return
}

func (m *Machine) RunCommands(addr []netip.Addr) error {
	cmds := []*CommandDescription{}
	cmds = append(cmds, m.CommandsPre...)
	if m.runCreation {
		cmds = append(cmds, m.Creation...)
	}
	if m.runStartup {
		cmds = append(cmds, m.Startup...)
	}
	if m.runCreation {
		cmds = append(cmds, m.CreationPost...)
	}
	cmds = append(cmds, m.Commands...)
	for _, cmd := range cmds {
		err := cmd.Run(m.Fqdn, addr)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Machine) RemoveMounts(log *slog.Logger) (changed bool, err error) {
	for _, mnt := range m.Mounts {
		var c bool
		c, err = mnt.RemoveMount(log)
		if err != nil {
			return
		}
		if c {
			changed = true
		}
	}
	return
}

func (m *Machine) Unmount(manager machineutil.MachineUtil) error {
	for _, mnt := range m.Mounts {
		job, err := manager.Stop(mnt.Unit())
		if err != nil {
			return err
		}
		err = job.Wait()
		if err != nil {
			return err
		}
	}
	return nil
}

type Config struct {
	DefaultTemplate string
	Machines        []*Machine
}

type ConfigDecoder interface {
	Decode(interface{}) error
}

type State struct {
	Manager   machineutil.MachineUtil
	Machines  map[string]*machineutil.Machine
	Templates machineutil.TemplateCollection
}

func NewState(config *Config) (retval *State, err error) {
	retval = &State{
		Machines: make(map[string]*machineutil.Machine),
	}
	retval.Manager, err = machineutil.NewMachineUtil()
	if err != nil {
		return
	}
	retval.Templates, err = retval.Manager.ListTemplates(config.DefaultTemplate)
	return
}

func (s *State) DiscoverTemplate(config *Machine) (*machineutil.Template, error) {
	var template *machineutil.Template
	if config.Template == "" {
		template = s.Templates.Template()
	} else {
		template = s.Templates.Get(config.Template)
	}
	if template == nil {
		return nil, fmt.Errorf("Missing template(%s) creating %s", config.Template, config.Fqdn)
	}
	return template, nil
}

func (s *State) EnsureMachine(log *slog.Logger, config *Machine, template *machineutil.Template) (machine *machineutil.Machine, changed bool, reload bool, err error) {
	changed = false
	reload = false
	var ok bool
	machine, ok = s.Machines[config.Fqdn]
	if ok {
		log.Debug("Already found")
		return
	}
	log.Debug("Fetching machine")
	machine, err = s.Manager.GetMachine(config.Fqdn)
	if err != nil && !errors.Is(err, machineutil.ErrNoSuchImage) {
		return
	}
	if errors.Is(err, machineutil.ErrNoSuchImage) && template != nil {
		log.Info("Creating machine")
		machine, err = template.Create(config.Fqdn)
		config.runCreation = true
		changed = true
	}
	if err != nil {
		return
	}
	s.Machines[config.Fqdn] = machine
	if template != nil {
		log.Info("Checking machine config")
		ok, err = machine.EnsureOptions(log, config.Options)
		if err != nil {
			return
		}
		changed = changed || ok
		ok, err = machine.EnsureOverride(log, config.Overrides)
		if err != nil {
			return
		}
		changed = changed || ok
		reload = reload || ok
		var mounts_changed bool
		mounts_changed, err = config.EnsureMounts(log)
		if err != nil {
			return
		}
		changed = changed || mounts_changed
		reload = reload || mounts_changed
		if changed {
			err = machine.Stop()
			if err != nil {
				return
			}
		}
		if mounts_changed {
			err = config.Unmount(s.Manager)
			if err != nil {
				return
			}
		}
	}
	if err == nil {
		s.Machines[config.Fqdn] = machine
		return
	}
	return
}

func (s *State) RemoveMachine(log *slog.Logger, config *Machine) error {
	machine, _, _, err := s.EnsureMachine(log, config, nil)
	if errors.Is(err, machineutil.ErrNoSuchImage) {
		return nil
	}
	delete(s.Machines, config.Fqdn)
	err = machine.Remove()
	if err != nil {
		return err
	}
	err = config.Unmount(s.Manager)
	if err != nil {
		return err
	}
	c, err := config.RemoveMounts(log)
	if err != nil {
		return err
	}
	if c {
		return s.Manager.DaemonReload()
	}
	return nil
}

func main() {
	configFile := flag.String("config", "-", "Config file to use")
	mode := flag.String("mode", "create", "Mode to use: create, start, stop, destroy")
	debug := flag.Bool("debug", false, "Enable debug log")
	flag.Parse()
	var err error
	log_options := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if *debug {
		log_options.Level = slog.LevelDebug
	}
	slog.SetDefault(
		slog.New(
			slog.NewTextHandler(
				os.Stderr,
				log_options,
			),
		),
	)
	switch *mode {
	case "create", "start", "stop", "destroy":
	default:
		slog.Error("Invalid mode", "mode", *mode)
		slog.Info("Try: create, start, stop, destroy")
		os.Exit(1)
	}
	slog.Info("Starting with mode", "mode", *mode)
	var configReader io.Reader
	switch *configFile {
	case "-":
		slog.Info("Reading config from stdin")
		configReader = os.Stdin
	default:
		slog.Info("Reading config from", "file", *configFile)
		configReader, err = os.Open(*configFile)
		if err != nil {
			slog.Error("Error opening config file", "file", *configFile, "error", err)
			os.Exit(1)
		}
	}
	var configDecoder ConfigDecoder
	switch path.Ext(*configFile) {
	case "json":
		slog.Info("Using json decoder")
		configDecoder = json.NewDecoder(configReader)
	default:
		slog.Info("Using yaml decoder")
		configDecoder = yaml.NewDecoder(configReader)
	}
	config := &Config{}
	slog.Info("Decoding config")
	err = configDecoder.Decode(&config)
	if err != nil {
		slog.Error("Error decoding config file", "file", *configFile, "error", err)
		os.Exit(1)
	}
	slog.Info("Creating state")
	state, err := NewState(config)
	if err != nil {
		slog.Error("Error creating state", "error", err)
		os.Exit(1)
	}
	base_log := slog.Default().With("mode", *mode)
	base_log.Info("Starting execution")
	for _, m := range config.Machines {
		log := base_log.With("machine", m.Fqdn)
		err := m.Normalize()
		if err != nil {
			log.Error("Normalizing config", "error", err)
			os.Exit(1)
		}
		if *mode == "destroy" {
			log.Info("Removing")
			err := state.RemoveMachine(log, m)
			if err != nil {
				log.Error("Removing", "error", err)
				os.Exit(1)
			}
			continue
		}
		var template *machineutil.Template
		if *mode == "create" {
			template, err = state.DiscoverTemplate(m)
			if err != nil {
				log.Error("Discovering template", "error", err)
				os.Exit(1)
			}
		}
		log.Info("Detecting machine")
		machine, _, reload, err := state.EnsureMachine(log, m, template)
		if *mode == "stop" {
			if errors.Is(err, machineutil.ErrNoSuchImage) {
				log.Warn("Missing")
				continue
			}
		}
		if err != nil {
			log.Error("Detecting", "error", err)
			os.Exit(1)
		}
		log.Info("Found")
		if *mode == "stop" {
			log.Info("Stopping")
			err = machine.Stop()
			if err != nil {
				log.Error("Stopping", "error", err)
				os.Exit(1)
			}
			err = m.Unmount(state.Manager)
			if err != nil {
				log.Error("Unmounting failed", "error", err)
				os.Exit(1)
			}
			continue
		}
		if reload {
			err := state.Manager.DaemonReload()
			if err != nil {
				log.Error("Failed to reload daemon", "error", err)
				os.Exit(1)
			}
		}
		if !machine.Running() {
			log.Info("Starting")
			err = machine.Start()
			m.runStartup = true
			if err != nil {
				log.Error("Starting", "error", err)
				os.Exit(1)
			}
		}
		log.Info("Waiting for address")
		addr, err := machine.WaitForAddress()
		if err != nil {
			log.Error("Wait address", "error", err)
			os.Exit(1)
		}
		err = m.RunCommands(addr)
		if err != nil {
			log.Error("Startup commands failed", "error", err)
			os.Exit(1)
		}
	}
	base_log.Info("Done.")
}
