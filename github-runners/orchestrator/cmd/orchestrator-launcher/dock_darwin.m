// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

#import <AppKit/AppKit.h>

// Implemented in Go (see dock_darwin.go).
extern void launcherMain(void);
extern void launcherWillTerminate(void);

@interface OrchestratorLauncherDelegate : NSObject <NSApplicationDelegate>
@end

@implementation OrchestratorLauncherDelegate
- (void)applicationDidFinishLaunching:(NSNotification *)note {
	// Run the launcher's work off the main thread so the run loop stays
	// responsive (and the Dock icon does not bounce).
	dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
		launcherMain();
	});
}
- (void)applicationWillTerminate:(NSNotification *)note {
	launcherWillTerminate();
}
@end

static OrchestratorLauncherDelegate *gDelegate;

// runCocoaApp registers as a regular (Dock-visible) app and runs the event loop.
// Running the loop is what stops the Dock icon bouncing.
void runCocoaApp(void) {
	[NSApplication sharedApplication];
	[NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
	gDelegate = [[OrchestratorLauncherDelegate alloc] init];
	[NSApp setDelegate:gDelegate];
	[NSApp activateIgnoringOtherApps:YES];
	[NSApp run];
}

// stopCocoaApp terminates the app from a background thread by hopping to the main
// queue. NSApp terminate: triggers applicationWillTerminate.
void stopCocoaApp(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[NSApp terminate:nil];
	});
}
