package control

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// frigateInput is one ffmpeg input reostream wants a Frigate camera to read.
type frigateInput struct {
	Path  string
	Roles []string
}

// mergeFrigate points the cameras in a pasted Frigate config at reostream:
// each camera named like a reostream camera gets its ffmpeg inputs
// replaced, and reostream cameras the config does not have are added.
// Everything else in the file -- other cameras, detectors, comments,
// order -- is left as it was. It reports which cameras it changed.
func mergeFrigate(src []byte, want map[string][]frigateInput, order []string) (out []byte, updated, added []string, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, nil, nil, fmt.Errorf("not valid YAML: %w", err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, nil, fmt.Errorf("the config is not a YAML mapping")
	}
	cameras := mapGet(root, "cameras")
	if cameras == nil {
		cameras = &yaml.Node{Kind: yaml.MappingNode}
		mapSet(root, "cameras", cameras)
	}
	if cameras.Kind != yaml.MappingNode {
		return nil, nil, nil, fmt.Errorf("cameras: is not a mapping")
	}
	for _, name := range order {
		inputs := inputsNode(want[name])
		cam := mapGet(cameras, name)
		if cam == nil || cam.Kind != yaml.MappingNode {
			cam = &yaml.Node{Kind: yaml.MappingNode}
			mapSet(cameras, name, cam)
			added = append(added, name)
		} else {
			updated = append(updated, name)
		}
		ff := mapGet(cam, "ffmpeg")
		if ff == nil || ff.Kind != yaml.MappingNode {
			ff = &yaml.Node{Kind: yaml.MappingNode}
			mapSet(cam, "ffmpeg", ff)
		}
		mapSet(ff, "inputs", inputs)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, nil, nil, err
	}
	enc.Close()
	return buf.Bytes(), updated, added, nil
}

func inputsNode(in []frigateInput) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, i := range in {
		roles := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, r := range i.Roles {
			roles.Content = append(roles.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: r})
		}
		item := &yaml.Node{Kind: yaml.MappingNode}
		mapSet(item, "path", &yaml.Node{Kind: yaml.ScalarNode, Value: i.Path})
		mapSet(item, "roles", roles)
		seq.Content = append(seq.Content, item)
	}
	return seq
}

// mapGet finds key in a YAML mapping node.
func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapSet replaces key's value in a mapping node, or appends it.
func mapSet(m *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = v
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
}
