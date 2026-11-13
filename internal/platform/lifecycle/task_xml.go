package lifecycle

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
)

const taskNamespace = "http://schemas.microsoft.com/windows/2004/02/mit/task"

// stableTaskXML ignores only the root Task/Settings/Enabled text, not triggers/actions.
func stableTaskXML(text string) (string, error) {
	if len(text) == 0 || len(text) > 1<<20 {
		return "", errors.New("invalid task XML size")
	}
	d := xml.NewDecoder(strings.NewReader(text))
	var b bytes.Buffer
	e := xml.NewEncoder(&b)
	var path []xml.Name
	seenRoot := false
	enabledDepth := 0
	for {
		t, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch x := t.(type) {
		case xml.Directive:
			return "", errors.New("task XML directives not allowed")
		case xml.StartElement:
			if enabledDepth > 0 {
				return "", errors.New("nested task Enabled value")
			}
			path = append(path, x.Name)
			if len(path) == 1 {
				if seenRoot || x.Name.Local != "Task" || x.Name.Space != taskNamespace {
					return "", errors.New("invalid task XML root")
				}
				seenRoot = true
			}
			if len(path) == 3 && path[0].Local == "Task" && path[1].Local == "Settings" && path[1].Space == taskNamespace && x.Name.Local == "Enabled" && x.Name.Space == taskNamespace {
				enabledDepth = 3
			}
		case xml.CharData:
			if enabledDepth > 0 {
				continue
			}
		case xml.EndElement:
			if len(path) == enabledDepth && enabledDepth > 0 {
				enabledDepth = 0
			}
			if len(path) == 0 {
				return "", errors.New("unbalanced XML")
			}
			path = path[:len(path)-1]
		}
		if err = e.EncodeToken(t); err != nil {
			return "", err
		}
	}
	if !seenRoot || len(path) != 0 {
		return "", errors.New("incomplete task XML")
	}
	if err := e.Flush(); err != nil {
		return "", err
	}
	return b.String(), nil
}
func sameTaskDefinition(a, b TaskValue) bool {
	ax, e := stableTaskXML(a.XML)
	if e != nil {
		return false
	}
	bx, e := stableTaskXML(b.XML)
	if e != nil || ax != bx {
		return false
	}
	a.Enabled = b.Enabled
	a.XML = b.XML
	return a == b
}
