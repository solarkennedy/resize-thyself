//#! /usr/bin/env gorun
package main

import (
	"fmt"
		"github.com/aws/aws-sdk-go/aws"

"time"
		"github.com/aws/aws-sdk-go/service/ec2"

	"github.com/aws/aws-sdk-go/aws/session"

	"github.com/aws/aws-sdk-go/aws/ec2metadata"

	"github.com/docopt/docopt-go"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const volumeIncreasePercent = 0.2

var version string

func parseArgs() map[string]interface{} {
	usage := `resize-thyself - Automatically resize a block device under pressue
Usage:
  resize-theyself [-v] [-d]
Options:
  -v, --verbose                   Be more verbose [default: false]
	-d, --dryrun                    Dry run (don't resize) [default: false]
  -h, --help     Show this screen
  --version     Show version
`

	arguments, _ := docopt.Parse(usage, nil, true, version, false)
	return arguments
}

func safeRun(command []string, dryrun bool) string {
	var (
		cmdOut []byte
		err    error
	)
	commandString := strings.Join(command, " ")
	if dryrun == true {
		log.Println("Would run: %s", commandString)
		return ""
	}
		if cmdOut, err = exec.Command(command[0], command[1:]...).Output(); err != nil {
			log.Println("There was an error running %s\n", commandString)
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return string(cmdOut)
}



func parseDfOutput(dfOutput string) (float64, error) {
	lines := strings.Split(dfOutput, "\n")
	// The Header is the first line, our df should be the second line
	dfLine := lines[1]

	parsedLine := strings.Fields(dfLine)
	log.Println(parsedLine)
	total, err := strconv.ParseFloat(parsedLine[1], 64)
	if err != nil {
		return 0, err
	}
	used, err := strconv.ParseFloat(parsedLine[2], 64)
	if err != nil {
		return 0, err
	}
	return used / total, nil

}

func getEbsBlockDevices() []string {
	md := ec2metadata.New(session.New())
	if md.Available() == true {
		mapping, _ := md.GetMetadata("block-device-mapping")
		log.Println(mapping)
		// TODO: Filter only EBS
		return []string{"/dev/xvda"}
}
		log.Println("ec2 metadata not available.")
		return []string{}

}

func lookupMount(device string) (string, string) {
	partition := "/dev/xvda1"
	mount := "/"
	return partition, mount
}

func mountNeedsResizing(mount string) bool {
	df := safeRun([]string{"df", mount}, false)
	percentUsed, _ := parseDfOutput(df)
	log.Println(mount, " has a usage of ", percentUsed)
	return percentUsed > 0.9
}

func isModificiationComplete(state *ec2.VolumeModification) bool {
	return aws.StringValue(state.ModificationState) == ec2.VolumeModificationStateCompleted
}

func waitForResize(d string) {
	complete := false
	for complete == false {
		time.Sleep(60*time.Second)
	volumeModification, err := d.describeVolumeModification()
	if err != nil {
		panic(err)
		}
		complete = isModificiationComplete(volumeModification)
	}
}

func resizeEbsDevice(device string) {
	log.Printf("Resizing EBS device %s!\n", device)
	volumeID := "TBD"
	existingSize := aws.Int64(0) // TBD
	newSize := (existingSize) * (1.00 + volumeIncreasePercent)
	request := &ec2.ModifyVolumeInput{
		VolumeId: volumeID.awsString(volumeID),
		Size:     aws.Int64(newSize),
	}
	output, err := d.ec2.ModifyVolume(request)
	if err != nil {
		log.Panicf("AWS modifyVolume failed for %s with %v", volumeID, err)
	}
	volumeModification := output.VolumeModification
	if isModificiationComplete(volumeModification) {
		return
	}
		waitForResize(volumeID)
		return

}

func growPartition(partition string) {
	log.Printf("growing parition %s!\n", partition)
	safeRun([]string{"growpart", partition}, true)
}

func resizeFilesystem(partition string) {
	log.Printf("Resizing filesystem on partition %s!\n", partition)
	safeRun([]string{"resize2fs", partition}, true)
}

func main() {
	args := parseArgs()
	verbose := args["--verbose"].(bool)
	dryRun := args["--dryrun"].(bool)


	EbsBlockDevices := getEbsBlockDevices()
	for _, device := range EbsBlockDevices {
		log.Printf("Inspecting %s\n", device)
		mount, partition := lookupMount(device)
		if mountNeedsResizing(mount) {
			resizeEbsDevice(device)
			growPartition(partition)
			resizeFilesystem(partition)
		}
	}
}
