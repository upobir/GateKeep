#import <ServiceManagement/ServiceManagement.h>
#include <string.h>
#include "startup_darwin.h"

int startupStatus(void) {
	if (@available(macOS 13.0, *)) {
		return (int)SMAppService.mainAppService.status;
	}
	return 0;
}

char *startupSet(int enabled) {
	if (@available(macOS 13.0, *)) {
		NSError *error = nil;
		BOOL ok = enabled ? [SMAppService.mainAppService registerAndReturnError:&error]
		                  : [SMAppService.mainAppService unregisterAndReturnError:&error];
		if (ok) {
			return NULL;
		}
		if (error.localizedDescription.length == 0) {
			return strdup("could not update startup at login");
		}
		return strdup(error.localizedDescription.UTF8String);
	}
	return strdup("startup at login requires macOS 13");
}
