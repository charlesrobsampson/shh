// Hides the operating system's notification banners while the notification database still records them (docs/spikes/02-focus.md).
//
// Notification Center shows each banner in a window of its own process. Moving that window off screen hides the banner without
// dismissing it, so macOS still writes the record shh reads. Banners whose title starts with the prefix in SHH_OWN_PREFIX are shh's
// own fallback banners and are left where they are.
ObjC.import('Foundation');

const env = $.NSProcessInfo.processInfo.environment;
const ownPrefix = env.objectForKey('SHH_OWN_PREFIX').js || '[shh]';
const offscreen = [6000, 6000];

const center = Application('System Events').processes.byName('NotificationCenter');

function bannerTitles(element, out, depth) {
  if (depth > 9) return;
  try {
    if (element.role() === 'AXStaticText' && element.attributes.byName('AXIdentifier').value() === 'title') out.push(String(element.value()));
  } catch (e) {}
  try { element.uiElements().forEach(child => bannerTitles(child, out, depth + 1)); } catch (e) {}
}

// If shh dies without stopping this script (kill -9, a crash), it must not go on hiding banners with nobody in charge:
// leave as soon as the parent process changes, putting any parked window back first.
ObjC.import('unistd');
const parent = $.getppid();

function restoreBanners() {
  try {
    const windows = center.windows();
    for (let i = 0; i < windows.length; i++) {
      if (windows[i].position()[0] >= 5000) windows[i].position = [0, 0];
    }
  } catch (e) {}
}

while ($.getppid() === parent) {
  try {
    const windows = center.windows();
    for (let i = 0; i < windows.length; i++) {
      const titles = [];
      bannerTitles(windows[i], titles, 0);
      const keep = titles.some(title => title.indexOf(ownPrefix) === 0);
      const parked = windows[i].position()[0] >= 5000;
      if (keep && parked) windows[i].position = [0, 0];
      else if (!keep && !parked) windows[i].position = offscreen;
    }
  } catch (e) {
    // Notification Center restarting, or nothing on screen: try again shortly.
  }
  $.NSThread.sleepForTimeInterval(0.05);
}

restoreBanners();
