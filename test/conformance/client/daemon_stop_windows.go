package main

// stopDaemon is a no-op on Windows, where the conformance suite doesn't run.
func stopDaemon(string) {}
