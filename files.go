// Copyright (c) bwplotka/mimic Authors
// Licensed under the Apache License 2.0.

package mimic

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/philipgough/mimic/encoding"
)

const GeneratedComment = "Generated by mimic. DO NOT EDIT."

// FilePool is a struct for storing and managing files to be generated as part of generation.
type FilePool struct {
	Logger log.Logger

	path []string

	m map[string]string

	topLevelComments []string
}

// Add adds a file to the file pool at the current path. The file is identified by filename.
// Content of the file is passed via an io.Reader.
//
// If the file with the given name has already been added at this path the code will `panic`.
// NOTE: See mimic/encoding for different marshallers to use as io.Reader.
func (f *FilePool) Add(fileName string, e encoding.Encoder) {
	if filepath.Base(fileName) != fileName {
		Panicf("")
	}

	b, err := io.ReadAll(e)
	if err != nil {
		Panicf("failed to output: %s", err)
	}

	if len(f.topLevelComments) > 0 {
		commentBytes := []byte{}
		for _, comment := range f.topLevelComments {
			commentBytes = append(commentBytes, e.EncodeComment(comment)...)
		}
		b = append(commentBytes, b...)
	}

	output := filepath.Join(append(f.path, fileName)...)

	// Check whether we have already written something into this file.
	if _, ok := f.m[output]; ok {
		Panicf("filename clash: %s", output)
	}

	if f.m == nil {
		f.m = make(map[string]string)
	}
	f.m[output] = string(b)
}

func (f *FilePool) write(outputDir string) {
	for file, contents := range f.m {
		out := filepath.Join(outputDir, file)
		if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
			PanicErr(fmt.Errorf("create directory %s: %w", filepath.Dir(out), err))
		}

		// TODO(https://github.com/bwplotka/mimic/issues/11): Diff the things if something is already here and remove.

		_ = level.Debug(f.Logger).Log("msg", "writing file", "file", out)
		if err := os.WriteFile(out, []byte(contents), 0755); err != nil {
			PanicErr(fmt.Errorf("write file to %s: %w", out, err))
		}
	}
}
