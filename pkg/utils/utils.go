package utils

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func AbsFilePath(fileName string) (string, error) {
	var fullFilePath string
	projectRoot, err := ProjectRoot()
	if err != nil {
		return "", err
	}

	err = filepath.Walk(projectRoot, func(path string, info fs.FileInfo, err error) error {
		if !info.IsDir() && strings.HasPrefix(info.Name(), fileName) {
			fullFilePath = path //filepath.Join(projectRoot, info.Name())
			return nil
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fullFilePath, nil
}

func ProjectRoot() (string, error) {
	_, currentPath, _, _ := runtime.Caller(0) //start path
	//find the project rot
	var err error
	var dir string

	dir, err = filepath.Abs(currentPath)
	if err != nil {
		return "", err
	}
	for {
		if _, err = os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not find go.mod")
}
