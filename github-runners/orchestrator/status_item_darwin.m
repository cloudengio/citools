// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

//go:build darwin

#import <AppKit/AppKit.h>
#import <CoreGraphics/CoreGraphics.h>

extern void goStatusItemOpenWebUI(void);
extern void goStatusItemViewLogs(void);
extern void goStatusItemRestart(void);
extern void goStatusItemUninstall(void);
extern void goStatusItemQuit(void);

@interface StatusItemMenuDelegate : NSObject <NSMenuDelegate>
- (instancetype)initWithServiceInstalled:(BOOL)installed;
- (void)updateStatusText:(NSString *)text;
@end

static StatusItemMenuDelegate *gMenuDelegate = nil;
static NSStatusItem *gStatusItem = nil;

@implementation StatusItemMenuDelegate {
    NSMenu *_menu;
    NSMenuItem *_statusItemRow;
    NSMenuItem *_restartItem;
    NSMenuItem *_uninstallItem;
}

- (instancetype)initWithServiceInstalled:(BOOL)installed {
    self = [super init];
    if (self) {
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

        _menu = [[NSMenu alloc] initWithTitle:@"GitHub Runner Orchestrator"];

        NSMenuItem *titleItem = [[NSMenuItem alloc] initWithTitle:@"GitHub Runner Orchestrator" action:nil keyEquivalent:@""];
        [titleItem setEnabled:NO];
        [_menu addItem:titleItem];

        _statusItemRow = [[NSMenuItem alloc] initWithTitle:@"Status: Running" action:nil keyEquivalent:@""];
        [_statusItemRow setEnabled:NO];
        [_menu addItem:_statusItemRow];

        [_menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *openWeb = [[NSMenuItem alloc] initWithTitle:@"Open Web UI" action:@selector(onOpenWeb:) keyEquivalent:@""];
        [openWeb setTarget:self];
        [_menu addItem:openWeb];

        NSMenuItem *viewLogs = [[NSMenuItem alloc] initWithTitle:@"View Logs..." action:@selector(onViewLogs:) keyEquivalent:@""];
        [viewLogs setTarget:self];
        [_menu addItem:viewLogs];

        [_menu addItem:[NSMenuItem separatorItem]];

        _restartItem = [[NSMenuItem alloc] initWithTitle:@"Restart Service..." action:@selector(onRestartService:) keyEquivalent:@""];
        [_restartItem setTarget:self];
        [_restartItem setEnabled:installed];
        [_menu addItem:_restartItem];

        _uninstallItem = [[NSMenuItem alloc] initWithTitle:@"Uninstall Service..." action:@selector(onUninstallService:) keyEquivalent:@""];
        [_uninstallItem setTarget:self];
        [_uninstallItem setEnabled:installed];
        [_menu addItem:_uninstallItem];

        [_menu addItem:[NSMenuItem separatorItem]];

        NSMenuItem *quit = [[NSMenuItem alloc] initWithTitle:@"Quit Orchestrator" action:@selector(onQuit:) keyEquivalent:@"q"];
        [quit setTarget:self];
        [_menu addItem:quit];

        gStatusItem.menu = _menu;
    }
    return self;
}

- (void)updateStatusText:(NSString *)text {
    _statusItemRow.title = text;
}

- (void)onOpenWeb:(id)sender {
    goStatusItemOpenWebUI();
}

- (void)onViewLogs:(id)sender {
    goStatusItemViewLogs();
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
        goStatusItemRestart();
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
        goStatusItemUninstall();
    }
}

- (void)onQuit:(id)sender {
    goStatusItemQuit();
}

@end

int checkGUIAvailable(void) {
    NSDictionary *session = (__bridge_transfer NSDictionary *)CGSessionCopyCurrentDictionary();
    if (!session) {
        return 0;
    }
    return 1;
}

void initAndRunCocoaApp(int serviceInstalled) {
    [NSApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    gMenuDelegate = [[StatusItemMenuDelegate alloc] initWithServiceInstalled:(serviceInstalled != 0)];
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
