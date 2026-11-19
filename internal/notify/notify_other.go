//go:build !darwin

package notify

func start() {
	markReady()
}

func deliver(string, string, string) {}
