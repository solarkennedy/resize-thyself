package main

import (
	"testing"

	"gotest.tools/assert"
)

func TestParsePartitionIntoDeviceAndNumber(t *testing.T) {
	actualD, actualN := parsePartitionIntoDeviceAndNumber("/dev/sda1")
	assert.Equal(t, actualD, "/dev/sda")
	assert.Equal(t, actualN, "1")

	actualD, actualN = parsePartitionIntoDeviceAndNumber("/dev/nvme0n1p1")
	assert.Equal(t, actualD, "/dev/nvme0n1")
	assert.Equal(t, actualN, "1")
}
