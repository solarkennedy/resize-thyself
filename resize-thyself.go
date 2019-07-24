//#! /usr/bin/env gorun
package main

import (
	"bytes"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	_ "github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/ec2metadata"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/docopt/docopt-go"
	"log"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const volumeIncreasePercent float64 = 0.2

var version string

func parseArgs() map[string]interface{} {
	usage := `resize-thyself - Automatically resize a block device under pressue
Usage:
  resize-thyself [-v] [-d] [--threshold=<percent>]
Options:
  --threshold=<percent>        How full should the disk be before acting? [default: 90]
  -v, --verbose                Be more verbose [default: false]
  -d, --dryrun                 Dry run (don't resize) [default: false]
  -h, --help                   Show this screen
  --version                    Show version
`
	arguments, _ := docopt.Parse(usage, nil, true, version, false)
	return arguments
}

func safeRun(command []string, dryrun bool) string {
	commandString := strings.Join(command, " ")
	if dryrun {
		log.Printf("Would run: '%s'\n", commandString)
		return ""
	}
	cmd := exec.Command(command[0], command[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	outStr, errStr := string(stdout.Bytes()), string(stderr.Bytes())

	fmt.Fprintln(os.Stderr, string(outStr))
	fmt.Fprintln(os.Stderr, string(errStr))
	if err != nil {
		exitError, _ := err.(*exec.ExitError)
		log.Printf("%s exited %d", commandString, exitError.ExitCode())
		log.Printf("There was an error running %s\n", commandString)
		log.Print(err)
		os.Exit(1)
	}
	return string(outStr)
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
	sess, _ := session.NewSession()
	md := ec2metadata.New(sess)
	if md.Available() {
		mapping, _ := md.GetMetadata("block-device-mapping")
		log.Println(mapping)
		// TODO: Filter only EBS
		return []string{"/dev/xvda"}
	}
	log.Println("ec2 metadata not available.")
	return []string{}

}

func lookupMount(device string) (string, string) {
	out := safeRun([]string{"grep", "^" + device, "/proc/mounts"}, false)
	numLines := strings.Count(out, "\n")
	if numLines != 1 {
		log.Printf("Ah! There was more than one mount with %v:\n%v", device, out)
		os.Exit(1)
	}
	split := strings.Split(out, " ")
	return split[0], split[1]
}

func mountNeedsResizing(mount string, threshold float64, verbose bool) bool {
	df := safeRun([]string{"df", mount}, false)
	percentUsed, _ := parseDfOutput(df)
	log.Println(mount, "has a usage of", percentUsed)
	return percentUsed > threshold
}

func isModificiationComplete(state *ec2.VolumeModification) bool {
	return aws.StringValue(state.ModificationState) == ec2.VolumeModificationStateCompleted
}

func describeVolumeModification(volumeID string, ec2Client *ec2.EC2) (*ec2.VolumeModification, error) {
	request := &ec2.DescribeVolumesModificationsInput{
		VolumeIds: []*string{&volumeID},
	}
	volumeMods, err := ec2Client.DescribeVolumesModifications(request)

	if err != nil {
		return nil, fmt.Errorf("error describing volume modification %s with %v", volumeID, err)
	}

	if len(volumeMods.VolumesModifications) == 0 {
		return nil, fmt.Errorf("no volume modifications found for %s", volumeID)
	}
	lastIndex := len(volumeMods.VolumesModifications) - 1
	return volumeMods.VolumesModifications[lastIndex], nil
}

func waitForResize(volumeID string, ec2Client *ec2.EC2) {
	complete := false
	for !complete {
		time.Sleep(60 * time.Second)
		volumeModification, err := describeVolumeModification(volumeID, ec2Client)
		if err != nil {
			panic(err)
		}
		complete = isModificiationComplete(volumeModification)
	}
}

func resizeEbsDevice(device string, ec2Client *ec2.EC2, dryRun bool) {
	log.Printf("Resizing EBS device %s!\n", device)
	volumeID := "TBD"
	var existingSize int64 // TBD
	newSize := int64(math.Round(float64(existingSize) * (1.00 + volumeIncreasePercent)))
	request := &ec2.ModifyVolumeInput{
		VolumeId: &volumeID,
		Size:     aws.Int64(newSize),
	}
	if !dryRun {
		output, err := ec2Client.ModifyVolume(request)
		if err != nil {
			log.Panicf("AWS modifyVolume failed for %s with %v", volumeID, err)
		}
		volumeModification := output.VolumeModification
		if isModificiationComplete(volumeModification) {
			return
		}
		waitForResize(volumeID, ec2Client)
		return
	}
}

func growPartition(partition string, dryRun bool) {
	log.Printf("Going to grow parition %s!\n", partition)
	safeRun([]string{"growpart", partition}, dryRun)
}

func resizeFilesystem(partition string, dryRun bool) {
	log.Printf("Going to resize filesystem on partition %s!\n", partition)
	safeRun([]string{"resize2fs", partition}, dryRun)
}

func main() {
	args := parseArgs()
	verbose := args["--verbose"].(bool)
	dryRun := args["--dryrun"].(bool)
	raw_threshold := args["--threshold"].(string)
	threshold, _ := strconv.ParseFloat(raw_threshold, 64)
	threshold = threshold / float64(100)

	sess, err := session.NewSession(&aws.Config{
		Region: aws.String("us-west-2")},
	)
	if err != nil {
		fmt.Printf("error creating AWS EC2 client: %v", err)
		os.Exit(1)
	}
	ec2Client := ec2.New(sess)

	EbsBlockDevices := getEbsBlockDevices()
	for _, device := range EbsBlockDevices {
		log.Printf("Inspecting %s\n", device)
		mount, partition := lookupMount(device)
		if mountNeedsResizing(mount, threshold, verbose) {
			resizeEbsDevice(device, ec2Client, dryRun)
			growPartition(partition, dryRun)
			resizeFilesystem(partition, dryRun)
		}
	}
}
