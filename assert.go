package main

import "fmt"

func assert(cond bool, msg string, args ...interface{}) {
	if !cond {
		panic(fmt.Sprintf(msg, args...))
	}
}
