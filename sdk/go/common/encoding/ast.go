// Copyright 2016-2021, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package encoding

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/pulumi/go-yaml/ast"
	"github.com/pulumi/go-yaml/parser"
	"github.com/pulumi/go-yaml/printer"
	"github.com/pulumi/go-yaml/token"
	"github.com/pulumi/pulumi/sdk/v2/go/common/resource/config"
)

type FileAST struct {
	ast *ast.File
}

func NewFileAST(yamlBytes []byte) (*FileAST, error) {
	if yamlBytes == nil {
		return &FileAST{}, nil
	}

	fileAST, err := parser.ParseBytes(yamlBytes, parser.ParseComments)
	if err != nil {
		return nil, errors.Wrap(err, "failed to parse YAML file")
	}

	return &FileAST{ast: fileAST}, nil
}

func (f *FileAST) IsEmpty() bool {
	return f.ast == nil
}

func (f *FileAST) Marshal() []byte {
	out := bytes.Buffer{}
	var p printer.Printer
	for _, d := range f.ast.Docs {
		out.Write(p.PrintNode(d))
	}

	return out.Bytes()
}

// TODO: should accept a Value instead so we can handle the secure marshalling
//       pretty sure this is also not handling encrypted objects correctly
func (f *FileAST) SetConfig(keyPath, key string, value config.Value, column int) error {
	if f.ast == nil {
		return nil
	}

	// TODO: probably want to handle this differently
	if len(f.ast.Docs) < 1 {
		return nil
	}

	// TODO: need to calculate the column based on the specified values indentation

	var paths []string
	if len(keyPath) > 0 {
		paths = strings.Split(keyPath, ".")
	}

	node := f.ast.Docs[0].Body.(*ast.MappingNode)
	var err error
	for _, path := range paths {
		node, err = walk(node, path)
		if err != nil {
			return errors.Wrap(err, "failed to set config")
		}
	}

	for _, v := range node.Values {
		if v.Key.String() == key {
			// Update the existing value
			if value.Secure() {
				v.Value = newMappingValueNode("secure", value.EncryptedValue(), column)
			} else {
				v.Value = newStringNode(value.EncryptedValue(), column)
			}
			return nil
		}
	}

	// Key not found, so create a new one
	secureMV := func() *ast.MappingValueNode {
		k := token.New(key, key, &token.Position{Column: column})
		v := token.New(value.EncryptedValue(), value.EncryptedValue(), &token.Position{Column: column + 4})
		secureToken := token.New("secure", "secure", &token.Position{Column: column + 2})
		return &ast.MappingValueNode{
			BaseNode: &ast.BaseNode{},
			Start:    k,
			Key:      ast.String(k),
			Value: &ast.MappingValueNode{
				BaseNode: &ast.BaseNode{},
				Start:    k,
				Key:      ast.String(secureToken),
				Value:    ast.String(v),
			},
		}
	}
	node.Values = append(node.Values, secureMV())
	return nil
}

func (f *FileAST) DeleteConfig(keyPath string, key string) error {
	if f.ast == nil {
		return nil
	}

	// TODO: probably want to handle this differently
	if len(f.ast.Docs) < 1 {
		return nil
	}

	var paths []string
	if len(keyPath) > 0 {
		paths = strings.Split(keyPath, ".")
	}

	node := f.ast.Docs[0].Body.(*ast.MappingNode)
	var err error
	for _, path := range paths {
		node, err = walk(node, path)
		if err != nil {
			return errors.Wrap(err, "failed to delete config")
		}
	}

	for i, v := range node.Values {
		if v.Key.String() == key {
			node.Values = append(node.Values[:i], node.Values[i+1:]...)
			return nil
		}
	}
	return nil
}

func walk(node *ast.MappingNode, key string) (*ast.MappingNode, error) {
	// TODO: handle slice key

	for _, v := range node.Values {
		if v.Key.String() == key {
			return v.Value.(*ast.MappingNode), nil
		}
	}
	return nil, fmt.Errorf("config key not found: %q", key)
}

func newMappingValueNode(k, v string, col int) *ast.MappingValueNode {
	key := token.New(k, k, &token.Position{Column: col})
	val := token.New(v, v, &token.Position{Column: col})
	return &ast.MappingValueNode{
		BaseNode: &ast.BaseNode{},
		Start:    key,
		Key:      ast.String(key),
		Value:    ast.String(val),
	}
}

func newStringNode(s string, column int) *ast.StringNode {
	return ast.String(token.New(s, s, &token.Position{Column: column}))
}
