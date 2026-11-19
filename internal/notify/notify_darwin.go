//go:build darwin

package notify

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications -Wl,-sectcreate,__TEXT,__info_plist,${SRCDIR}/Info.plist

#include <stdlib.h>
#include "notify_darwin.h"
*/
import "C"

import (
	"log"
	"os/exec"
	"unsafe"
)

func start() {
	C.notifyStart()
}

func deliver(title, body, url string) {
	cTitle := C.CString(title)
	cBody := C.CString(body)
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cTitle))
	defer C.free(unsafe.Pointer(cBody))
	defer C.free(unsafe.Pointer(cURL))
	C.notifyPost(cTitle, cBody, cURL)
}

//export notifyReady
func notifyReady() {
	markReady()
}

//export notifyOpened
func notifyOpened(url *C.char) {
	raw := C.GoString(url)
	go openApproval(raw)
}

func openApproval(raw string) {
	if err := exec.Command("open", raw).Start(); err != nil {
		log.Printf("open notification: %v", err)
	}
}
