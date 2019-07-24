# resize-thyself

[![Build Status](https://travis-ci.org/solarkennedy/resize-thyself.svg?branch=master)](https://travis-ci.org/solarkennedy/resize-thyself)
A tool to resize a servers disks when under disk pressure.

## About

We live in a modern world with "Cloud" providers and unlimited compute resources, why are we still fighting full disks?

Why can't disks truly be "elastic", and at least grow (not shrink!?) with usage.

Well with `resize-thyself` you can at least pretend. It resizes your disks when they start to get full.

## Objections

#### Why not provision enough disk in the first place?

Sometimes you don't know how much disk you are going to need till you need it!

Even if we could guess, it would be wasteful to provision it all at once. With `resize-thyself`, it grows "just in time".

#### What if a run-away process uses up all my disk and wastes tons of $$$?

Cap how much money you would like to spend with `--max-size` (defaults to `1TB`). *Then* you can get woken up in the middle of the night to a full disk.

Sorry though, you won't be able to shrink.

`resize-thyself` isn't for the cost-sensitive. If your developer time is costly, or it isn't worth the risk to an application crashing due to a full disk, then maybe it is worth it to just resize your disks when you need to.

## Install

    go install github.com/solarkennedy/resize-thyself

## Usage

```
$ resize-thyself
...
```

## IaaS Disk Support

[x] AWS EBS volumes
[ ] GCP Persistent disks
[ ] Azure managed disks
