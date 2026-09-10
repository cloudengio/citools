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

static NSString *safeString(const char *str) {
	if (!str) {
		return @"";
	}
	NSString *s = [NSString stringWithUTF8String:str];
	if (!s) {
		s = [[NSString alloc] initWithBytes:str length:strlen(str) encoding:NSISOLatin1StringEncoding];
	}
	return s ? s : @"";
}

int showNativeConfirm(const char *title, const char *message) {
	__block int result = 0;
	void (^block)(void) = ^{
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setMessageText:safeString(title)];
		[alert setInformativeText:safeString(message)];
		[alert addButtonWithTitle:@"Install"];
		[alert addButtonWithTitle:@"Not Now"];
		[alert setAlertStyle:NSAlertStyleInformational];
		[NSApp activateIgnoringOtherApps:YES];
		NSModalResponse resp = [alert runModal];
		result = (resp == NSAlertFirstButtonReturn) ? 1 : 0;
	};
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
	return result;
}

void showNativeNotify(const char *title, const char *message) {
	void (^block)(void) = ^{
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setMessageText:safeString(title)];
		[alert setInformativeText:safeString(message)];
		[alert addButtonWithTitle:@"OK"];
		[alert setAlertStyle:NSAlertStyleInformational];
		[NSApp activateIgnoringOtherApps:YES];
		[alert runModal];
	};
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}

void showNativeLogDialog(const char *title, const char *message, const char *logSnippet, const char *logPath) {
	void (^block)(void) = ^{
		NSAlert *alert = [[NSAlert alloc] init];
		[alert setMessageText:safeString(title)];
		[alert setInformativeText:safeString(message)];
		[alert setAlertStyle:NSAlertStyleWarning];
		[alert addButtonWithTitle:@"OK"];

		NSString *lp = safeString(logPath);
		BOOL hasLogPath = (lp.length > 0);
		if (hasLogPath) {
			[alert addButtonWithTitle:@"Open Full Log"];
		}

		NSRect scrollFrame = NSMakeRect(0, 0, 520, 180);
		NSScrollView *scrollView = [[NSScrollView alloc] initWithFrame:scrollFrame];
		[scrollView setHasVerticalScroller:YES];
		[scrollView setHasHorizontalScroller:YES];
		[scrollView setAutohidesScrollers:YES];
		[scrollView setBorderType:NSBezelBorder];

		NSTextView *textView = [[NSTextView alloc] initWithFrame:NSMakeRect(0, 0, scrollFrame.size.width, scrollFrame.size.height)];
		[textView setMinSize:NSMakeSize(0.0, scrollFrame.size.height)];
		[textView setMaxSize:NSMakeSize(FLT_MAX, FLT_MAX)];
		[textView setVerticallyResizable:YES];
		[textView setHorizontallyResizable:YES];
		[textView setAutoresizingMask:(NSViewWidthSizable | NSViewHeightSizable)];
		[[textView textContainer] setContainerSize:NSMakeSize(FLT_MAX, FLT_MAX)];
		[[textView textContainer] setWidthTracksTextView:NO];
		[textView setEditable:NO];
		[textView setSelectable:YES];

		NSFont *font = nil;
		if (@available(macOS 10.15, *)) {
			font = [NSFont monospacedSystemFontOfSize:11.0 weight:NSFontWeightRegular];
		}
		if (!font) {
			font = [NSFont userFixedPitchFontOfSize:11.0];
		}
		if (font) {
			[textView setFont:font];
		}
		[textView setString:safeString(logSnippet)];
		[textView scrollRangeToVisible:NSMakeRange([[textView string] length], 0)];

		[scrollView setDocumentView:textView];
		[alert setAccessoryView:scrollView];

		[NSApp activateIgnoringOtherApps:YES];
		NSModalResponse resp = [alert runModal];
		if (hasLogPath && resp == NSAlertSecondButtonReturn) {
			NSURL *url = [NSURL fileURLWithPath:lp];
			[[NSWorkspace sharedWorkspace] openURL:url];
		}
	};
	if ([NSThread isMainThread]) {
		block();
	} else {
		dispatch_sync(dispatch_get_main_queue(), block);
	}
}
