// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>

extern void goUIOpenWebUI(void);
extern void goUIViewLogs(void);
extern void goUIInstallService(void);
extern void goUIRestartService(void);
extern void goUIUninstallService(void);
extern void goUIQuit(void);
extern int goUIIsInstalled(void);

@interface StatusItemAppDelegate : NSObject <NSApplicationDelegate, NSMenuDelegate>
- (instancetype)initWithServiceInstalled:(BOOL)installed;
- (void)updateStatusText:(NSString *)text;
- (void)updateServiceItems:(BOOL)installed;
@end

static StatusItemAppDelegate *gAppDelegate = nil;
static NSStatusItem *gStatusItem = nil;

@implementation StatusItemAppDelegate {
    NSMenu *_menu;
    NSMenuItem *_statusItemRow;
    NSMenuItem *_installItem;
    NSMenuItem *_restartItem;
    NSMenuItem *_uninstallItem;
}

- (instancetype)initWithServiceInstalled:(BOOL)installed {
    self = [super init];
    if (self) {
        _menu = [[NSMenu alloc] initWithTitle:@"GitHub Runner Orchestrator"];
        [_menu setAutoenablesItems:NO];
        [_menu setDelegate:self];

        NSMenuItem *titleItem = [[NSMenuItem alloc] initWithTitle:@"GitHub Runner Orchestrator" action:nil keyEquivalent:@""];
        [titleItem setEnabled:NO];
        [_menu addItem:titleItem];

        _statusItemRow = [[NSMenuItem alloc] initWithTitle:@"Status: Running" action:nil keyEquivalent:@""];
        [_statusItemRow setEnabled:NO];
        [_menu addItem:_statusItemRow];

        [_menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *openWeb = [[NSMenuItem alloc] initWithTitle:@"Open Web UI" action:@selector(onOpenWeb:) keyEquivalent:@""];
        [openWeb setTarget:self];
        [openWeb setEnabled:YES];
        [_menu addItem:openWeb];

        NSMenuItem *viewLogs = [[NSMenuItem alloc] initWithTitle:@"View Logs..." action:@selector(onViewLogs:) keyEquivalent:@""];
        [viewLogs setTarget:self];
        [viewLogs setEnabled:YES];
        [_menu addItem:viewLogs];

        [_menu addItem:[NSMenuItem separatorItem]];

        _installItem = [[NSMenuItem alloc] initWithTitle:@"Install Service..." action:@selector(onInstallService:) keyEquivalent:@""];
        [_installItem setTarget:self];
        [_menu addItem:_installItem];

        _restartItem = [[NSMenuItem alloc] initWithTitle:@"Restart Service..." action:@selector(onRestartService:) keyEquivalent:@""];
        [_restartItem setTarget:self];
        [_menu addItem:_restartItem];

        _uninstallItem = [[NSMenuItem alloc] initWithTitle:@"Uninstall Service..." action:@selector(onUninstallService:) keyEquivalent:@""];
        [_uninstallItem setTarget:self];
        [_menu addItem:_uninstallItem];

        [self updateServiceItems:installed];

        [_menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *quit = [[NSMenuItem alloc] initWithTitle:@"Quit Orchestrator" action:@selector(onQuit:) keyEquivalent:@"q"];
        [quit setTarget:self];
        [quit setEnabled:YES];
        [_menu addItem:quit];
    }
    return self;
}

- (void)applicationDidFinishLaunching:(NSNotification *)note {
    gStatusItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
    if (@available(macOS 11.0, *)) {
        NSImage *img = [NSImage imageWithSystemSymbolName:@"server.rack" accessibilityDescription:@"GitHub Runner Orchestrator"];
        if (!img) {
            img = [NSImage imageWithSystemSymbolName:@"play.circle" accessibilityDescription:@"GitHub Runner Orchestrator"];
        }
        if (img) {
            [img setTemplate:YES];
            gStatusItem.button.image = img;
        } else {
            gStatusItem.button.title = @"[GH]";
        }
    } else {
        gStatusItem.button.title = @"[GH]";
    }
    gStatusItem.menu = _menu;
}

- (void)applicationWillTerminate:(NSNotification *)note {
    goUIQuit();
}

- (void)updateServiceItems:(BOOL)installed {
    [_installItem setHidden:installed];
    [_installItem setEnabled:!installed];
    [_restartItem setHidden:!installed];
    [_restartItem setEnabled:installed];
    [_uninstallItem setHidden:!installed];
    [_uninstallItem setEnabled:installed];
}

- (void)menuNeedsUpdate:(NSMenu *)menu {
    BOOL installed = (goUIIsInstalled() != 0);
    [self updateServiceItems:installed];
}

- (BOOL)validateMenuItem:(NSMenuItem *)menuItem {
    return [menuItem isEnabled];
}

- (void)updateStatusText:(NSString *)text {
    _statusItemRow.title = text;
}

- (void)onOpenWeb:(id)sender {
    goUIOpenWebUI();
}

- (void)onViewLogs:(id)sender {
    goUIViewLogs();
}

- (void)onInstallService:(id)sender {
    NSAlert *alert = [[NSAlert alloc] init];
    [alert setMessageText:@"Install GitHub Runner Orchestrator Service?"];
    [alert setInformativeText:@"This will register the orchestrator as a login service with launchd so it runs automatically when you log in."];
    [alert addButtonWithTitle:@"Install"];
    [alert addButtonWithTitle:@"Cancel"];
    [alert setAlertStyle:NSAlertStyleInformational];
    [NSApp activateIgnoringOtherApps:YES];
    if ([alert runModal] == NSAlertFirstButtonReturn) {
        goUIInstallService();
    }
}

- (void)onRestartService:(id)sender {
    NSAlert *alert = [[NSAlert alloc] init];
    [alert setMessageText:@"Restart GitHub Runner Orchestrator Service?"];
    [alert setInformativeText:@"Active workflow jobs may be interrupted. The service will restart automatically."];
    [alert addButtonWithTitle:@"Restart"];
    [alert addButtonWithTitle:@"Cancel"];
    [alert setAlertStyle:NSAlertStyleWarning];
    [NSApp activateIgnoringOtherApps:YES];
    if ([alert runModal] == NSAlertFirstButtonReturn) {
        goUIRestartService();
    }
}

- (void)onUninstallService:(id)sender {
    NSAlert *alert = [[NSAlert alloc] init];
    [alert setMessageText:@"Uninstall GitHub Runner Orchestrator Service?"];
    [alert setInformativeText:@"This will unload the login service from launchd, delete its configuration, and stop the orchestrator."];
    [alert addButtonWithTitle:@"Uninstall"];
    [alert addButtonWithTitle:@"Cancel"];
    [alert setAlertStyle:NSAlertStyleCritical];
    [NSApp activateIgnoringOtherApps:YES];
    if ([alert runModal] == NSAlertFirstButtonReturn) {
        goUIUninstallService();
    }
}

- (void)onQuit:(id)sender {
    goUIQuit();
}

@end

int checkGUIAvailable(void) {
    NSDictionary *session = (__bridge_transfer NSDictionary *)CGSessionCopyCurrentDictionary();
    if (!session) {
        return 0;
    }
    return 1;
}

void initAndRunCocoaApp(int mode, int serviceInstalled) {
    [NSApplication sharedApplication];
    if (mode == 1) { // ModeRegular
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];
    } else { // ModeAccessory
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    }
    gAppDelegate = [[StatusItemAppDelegate alloc] initWithServiceInstalled:(serviceInstalled != 0)];
    [NSApp setDelegate:gAppDelegate];
    [NSApp activateIgnoringOtherApps:YES];
    [NSApp run];
}

void stopCocoaApp(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp stop:nil];
        NSEvent *event = [NSEvent otherEventWithType:NSEventTypeApplicationDefined
                                            location:NSMakePoint(0, 0)
                                       modifierFlags:0
                                           timestamp:0
                                        windowNumber:0
                                             context:nil
                                             subtype:0
                                               data1:0
                                               data2:0];
        [NSApp postEvent:event atStart:YES];
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
