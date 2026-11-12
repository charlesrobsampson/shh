ObjC.import('Foundation');
ObjC.import('stdlib');
const env = $.NSProcessInfo.processInfo.environment;
const spec = env.objectForKey('HARNESS_SPEC').js;
const scriptPath = env.objectForKey('HARNESS_SCRIPT').js;
$.setenv('SHH_BANNER', spec, 1);
let text = $.NSString.stringWithContentsOfFileEncodingError(scriptPath, $.NSUTF8StringEncoding, $()).js;
text = text.replace("win.orderFront($());", "").replace(/\napp\.run;/, "\n").replace(/\nwin\.orderOut\(\$\(\)\);[\s\S]*$/, "\n");
text += `
;(function () {
  const frame = f => [f.origin.x, f.origin.y, f.size.width, f.size.height].map(v => Math.round(v * 10) / 10);
  const out = { banner: [cfg.width, height], title: frame(title.frame), body: frame(body.frame), header: header ? frame(header.frame) : null,
    buttons: buttons.map((b, i) => ({ type: cfg.buttons[i].type, label: saying[i], frame: frame(b.frame), face: backdrops[i] ? frame(backdrops[i].frame) : null, ink: inks[i] ? frame(inks[i].frame) : null })) };
  return JSON.stringify(out);
})()`;
eval(text);
