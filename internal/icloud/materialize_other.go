//go:build !darwin

package icloud

import "os"

func sourceIsDataless(os.FileInfo) bool { return false }
func requestSourceDownload(string)      {}
