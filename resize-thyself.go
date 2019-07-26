//#! /usr/bin/env gorun
package main

import (
	"bytes"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
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
  resize-thyself [-v] [-d] [--threshold=<percent>] [--grow-percent=<percent>]
Options:
  --threshold=<percent>        How full should the disk be before acting? [default: 90]
  --grow-percent=<percent>     How much should we grow the disk? [default: 10]
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

	if err != nil {
		exitError, _ := err.(*exec.ExitError)
		fmt.Fprintln(os.Stderr, string(outStr))
		fmt.Fprintln(os.Stderr, string(errStr))
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

func getRegion() string {
	sess, _ := session.NewSession()
	md := ec2metadata.New(sess)
	region, _ := md.Region()
	return region
}

func getInstanceID(ec2Client *ec2.EC2) string {
	sess, _ := session.NewSession()
	md := ec2metadata.New(sess)
	id, _ := md.GetMetadata("instance-id")
	return id
}

func getEbsBlockDevices() []string {
	sess, _ := session.NewSession()
	md := ec2metadata.New(sess)
	if md.Available() {
		mapping, _ := md.GetMetadata("block-device-mapping/root")
		log.Printf("Metadata mapping for root: '%+v'\n", mapping)
		// TODO: Filter only EBS, actually work, return more than the root
		return []string{mapping}
	}
	log.Println("ec2 metadata not available.")
	return []string{}

}

func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// Takes into account
// https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/device_naming.html
func mapEbsDeviceToLinuxDevice(ebsDevice string) string {
	if ebsDevice == "/dev/sda1" {
		if fileExists("/dev/nvme0n1p1") {
			return "/dev/nvme0n1p1"
		} else if fileExists("/dev/xvda1") {
			return "/dev/xvda1"
		} else if fileExists("/dev/sda1") {
			return "/dev/sda1"
		} else {
			log.Panicf("AWS says the EBS device should be %s, but that doesn't exist?", ebsDevice)
		}
	} else if ebsDevice == "/dev/xvda1" {
		if fileExists("/dev/nvme0n1p1") {
			return "/dev/nvme0n1p1"
		} else if fileExists("/dev/xvda1") {
			return "/dev/xvda1"
		} else {
			log.Panicf("AWS says the EBS device should be %s, but that doesn't exist?", ebsDevice)
		}
	} else {
		if fileExists(ebsDevice) {
			return ebsDevice
		} else {
			log.Panicf("It looks like %s doesn't exist on the system?", ebsDevice)
		}
	}
	return ebsDevice
}

func lookupMount(ebsDevice string) (string, string) {
	device := mapEbsDeviceToLinuxDevice(ebsDevice)
	out := safeRun([]string{"grep", "^" + device, "/proc/mounts"}, false)
	numLines := strings.Count(out, "\n")
	if numLines != 1 {
		log.Printf("Ah! There was more than one mount with %v:\n%v", device, out)
		os.Exit(1)
	}
	split := strings.Split(out, " ")
	partition := split[0]
	mount := split[1]
	return mount, partition
}

func mountNeedsResizing(mount string, threshold float64, verbose bool) bool {
	df := safeRun([]string{"df", mount}, false)
	percentUsed, _ := parseDfOutput(df)
	log.Printf("%s has a usage of %.2f%%", mount, percentUsed*100)
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
		log.Print("Sleeping 60 seconds, waiting for EBS volume resize to finish...")
		time.Sleep(60 * time.Second)
		volumeModification, err := describeVolumeModification(volumeID, ec2Client)
		if err != nil {
			panic(err)
		} else {
			fmt.Println(volumeModification)
		}
		complete = isModificiationComplete(volumeModification)
	}
}

func getEbsVolumeIDs(ec2Client *ec2.EC2, instanceID string) *ec2.DescribeVolumesOutput {
	input := &ec2.DescribeVolumesInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("attachment.instance-id"),
				Values: []*string{
					aws.String(instanceID),
				},
			},
		},
	}

	result, err := ec2Client.DescribeVolumes(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				fmt.Println(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			fmt.Println(err.Error())
		}
		return result
	}
	return result
}

func isEbsVolumeAttached(volume *ec2.Volume, ebsDevice string) bool {
	for _, attachment := range volume.Attachments {
		if *attachment.Device == ebsDevice {
			return true
		}
	}
	return false
}

func getEbsVolumeIDAndSize(ec2Client *ec2.EC2, instanceID string, ebsDevice string) (string, int64) {
	volumes := getEbsVolumeIDs(ec2Client, instanceID).Volumes
	for _, volume := range volumes {
		if isEbsVolumeAttached(volume, ebsDevice) {
			log.Printf("Looks like %s is attached to this instance %s as %s", *volume.VolumeId, instanceID, ebsDevice)
			return *volume.VolumeId, *volume.Size
		}
	}
	log.Fatalf("No volumes look attached: %v", volumes)
	return "", 0
}

func resizeEbsDevice(ebsDevice string, ec2Client *ec2.EC2, instanceID string, growPercent float64, dryRun bool) {
	log.Printf("Resizing EBS device '%s' by %.2f%%!\n", ebsDevice, growPercent*100)
	volumeID, existingSize := getEbsVolumeIDAndSize(ec2Client, instanceID, ebsDevice)
	newSize := int64(math.Round(float64(existingSize) * (1.00 + growPercent)))
	log.Printf("Growing EBS device '%s' by %.2f%% from %dGB to %dGB!\n", ebsDevice, growPercent*100, existingSize, newSize)
	request := &ec2.ModifyVolumeInput{
		VolumeId: &volumeID,
		Size:     aws.Int64(newSize),
		DryRun:   &dryRun,
	}
	output, err := ec2Client.ModifyVolume(request)
	if err != nil {
		if dryRun {
			log.Printf("AWS modifyVolume for %s returned with %v", volumeID, err)
		} else {
			log.Panicf("AWS modifyVolume failed for %s with %v", volumeID, err)
		}
	}

	if dryRun {
		return
	} else {
		time.Sleep(10 * time.Second)
		volumeModification := output.VolumeModification
		if isModificiationComplete(volumeModification) {
			return
		}
		waitForResize(volumeID, ec2Client)
		return
	}
}

func parsePartitionIntoDeviceAndNumber(partition string) (string, string) {
	device := partition[0 : len(partition)-1]
	partitionNumber := partition[len(partition)-1:]
	if _, err := strconv.Atoi(partitionNumber); err != nil {
		log.Panicf("%v doesn't looks like a number? Should be the partition number of %s\n", partitionNumber, partition)
	}
	lastCharOfDevice := device[len(device)-1:]
	if lastCharOfDevice == "p" {
		device = partition[0 : len(partition)-2]
		lastCharOfDevice = device[len(device)-1:]
	} else {
		if _, err := strconv.Atoi(lastCharOfDevice); err == nil {
			log.Panicf("%v ends in a number? Should just be the device part of %s\n", device, partition)
		}
	}
	return device, partitionNumber
}

func growPartition(partition string, dryRun bool) {
	device, partitionNumber := parsePartitionIntoDeviceAndNumber(partition)
	log.Printf("Going to grow parition %s!\n", partition)
	if dryRun {
		safeRun([]string{"growpart", "--dry-run", device, partitionNumber}, false)
	} else {
		safeRun([]string{"growpart", device, partitionNumber}, false)
	}
}

func resizeFilesystem(partition string, dryRun bool) {
	if dryRun {
		log.Printf("Would resize filesystem on partition %s\n", partition)
	} else {
		log.Printf("Going to resize filesystem on partition %s!\n", partition)
	}
	safeRun([]string{"resize2fs", partition}, dryRun)
}

func main() {
	args := parseArgs()
	verbose := args["--verbose"].(bool)
	dryRun := args["--dryrun"].(bool)

	raw_threshold := args["--threshold"].(string)
	threshold, _ := strconv.ParseFloat(raw_threshold, 64)
	threshold = threshold / float64(100)

	raw_grow_percent := args["--grow-percent"].(string)
	grow_percent, _ := strconv.ParseFloat(raw_grow_percent, 64)
	grow_percent = grow_percent / float64(100)

	region := getRegion()
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)
	if err != nil {
		fmt.Printf("error creating AWS EC2 client: %v", err)
		os.Exit(1)
	}
	ec2Client := ec2.New(sess)
	instanceID := getInstanceID(ec2Client)

	EbsBlockDevices := getEbsBlockDevices()
	for _, ebsDevice := range EbsBlockDevices {
		mount, partition := lookupMount(ebsDevice)
		log.Printf("Inspecting ebs device %s mounted on %s (real device name %s\n", ebsDevice, mount, partition)
		if mountNeedsResizing(mount, threshold, verbose) {
			log.Printf("%s is used more than our threshold (%.2f%%), resizing!", mount, threshold)
			resizeEbsDevice(ebsDevice, ec2Client, instanceID, grow_percent, dryRun)
			growPartition(partition, dryRun)
			resizeFilesystem(partition, dryRun)
		} else {
			log.Printf("%s doesn't need to be resized", mount)
		}
	}
}
