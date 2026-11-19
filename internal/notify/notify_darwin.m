#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>
#include "notify_darwin.h"

@interface GateKeepNotifier : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation GateKeepNotifier
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
       willPresentNotification:(UNNotification *)notification
         withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionSound | UNNotificationPresentationOptionList);
}

- (void)userNotificationCenter:(UNUserNotificationCenter *)center
didReceiveNotificationResponse:(UNNotificationResponse *)response
         withCompletionHandler:(void (^)(void))completionHandler {
	NSString *url = response.notification.request.content.userInfo[@"url"];
	if (url.length > 0) {
		notifyOpened((char *)url.UTF8String);
	}
	completionHandler();
}
@end

static GateKeepNotifier *gateKeepNotifier;

void notifyStart(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gateKeepNotifier == nil) {
			gateKeepNotifier = [GateKeepNotifier new];
		}
		UNUserNotificationCenter *center = UNUserNotificationCenter.currentNotificationCenter;
		center.delegate = gateKeepNotifier;
		[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
		                      completionHandler:^(BOOL granted, NSError *error) {
			if (!granted || error != nil) {
				NSBundle *bundle = NSBundle.mainBundle;
				NSLog(@"GateKeep notifications: granted=%d bundle=%@ id=%@ error=%@", granted, bundle.bundlePath, bundle.bundleIdentifier, error);
			}
			notifyReady();
		}];
	});
}

void notifyPost(char *title, char *body, char *url) {
	NSString *titleText = @(title);
	NSString *bodyText = @(body);
	NSString *urlText = @(url);
	dispatch_async(dispatch_get_main_queue(), ^{
		UNMutableNotificationContent *content = [UNMutableNotificationContent new];
		content.title = titleText;
		content.body = bodyText;
		content.sound = UNNotificationSound.defaultSound;
		if (urlText.length > 0) {
			content.userInfo = @{@"url": urlText};
		}
		UNNotificationRequest *request = [UNNotificationRequest requestWithIdentifier:@"pending"
		                                                                      content:content
		                                                                      trigger:nil];
		[UNUserNotificationCenter.currentNotificationCenter addNotificationRequest:request
		                                                     withCompletionHandler:^(NSError *error) {
			if (error != nil) {
				NSLog(@"GateKeep notification: %@", error);
			}
		}];
	});
}
