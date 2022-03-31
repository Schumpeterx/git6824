package raft

import (
	"log"
)

// Debugging
const Debug = true

// var logfile = flag.String("log", "test.log", "test")
func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}
