package machineutil

import (
	"strconv"

	"github.com/godbus/dbus/v5"
)

type TemplateCollection interface {
	Template() *Template
	Get(string) *Template
	Remove() error
}

type Template struct {
	Name    string
	Version int
	object  dbus.BusObject
	manager MachineUtil
}

var _ TemplateCollection = (*Template)(nil)

func (t *Template) Image() string { return t.Name + "-template_" + strconv.Itoa(t.Version) }

func (t *Template) Create(fqdn string) (*Machine, error) {
	return t.manager.Clone(t.Image(), fqdn)
}
func (t *Template) Remove() error {
	return t.manager.Remove(t.Image())
}
func (t *Template) Template() *Template {
	return t
}
func (t *Template) Get(name string) *Template {
	if t == nil || name != t.Name {
		return nil
	}
	return t
}

type TemplateVersions []*Template

var _ TemplateCollection = (*TemplateVersions)(nil)

func (t TemplateVersions) Len() int      { return len(t) }
func (t TemplateVersions) Swap(i, j int) { t[i], t[j] = t[j], t[i] }
func (t TemplateVersions) Less(i, j int) bool {
	if t[i].Name < t[j].Name {
		return true
	}
	if t[i].Name > t[j].Name {
		return false
	}
	return t[i].Version < t[j].Version
}
func (t TemplateVersions) Template() *Template {
	if t.Len() == 0 {
		return nil
	}
	return t[t.Len()-1]
}
func (t TemplateVersions) Remove() error {
	for _, template := range t {
		if err := template.Remove(); err != nil {
			return err
		}
	}
	return nil
}
func (t TemplateVersions) Get(name string) *Template {
	for i := t.Len(); i > 0; i-- {
		if img := t[i-1].Get(name); img != nil {
			return img
		}
	}
	return nil
}

type Templates struct {
	Default   string
	Templates map[string]TemplateVersions
}

var _ TemplateCollection = (*Templates)(nil)

func (t *Templates) Get(name string) *Template {
	if name == "" {
		name = t.Default
	}
	return t.Templates[name].Get(name)
}

func (t *Templates) Template() *Template {
	return t.Templates[t.Default].Template()
}

func (t *Templates) Remove() error {
	for _, templates := range t.Templates {
		if err := templates.Remove(); err != nil {
			return err
		}
	}
	return nil
}
