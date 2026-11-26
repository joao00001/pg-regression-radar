// Browser smoke test against a running `pg-regression-radar dashboard --fixture ...`.
// Not part of `go test` (the repo has no Node dependency): run it by hand.
//   NODE_PATH=$(npm root -g) DASHBOARD_URL=http://127.0.0.1:8765/ node internal/dashboard/uitest/smoke.playwright.cjs
// SHOTS=/some/dir also writes screenshots (light, dark, narrow) there.
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const url = (process.env.DASHBOARD_URL || 'http://127.0.0.1:8765/').replace(/\/$/, '') + '/';
const shots = process.env.SHOTS || '';

const panel = (id) => `.panel[data-panel="${id}"]`;

(async () => {
  const b = await chromium.launch();
  const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
  const p = await ctx.newPage();
  const errs = [];
  p.on('console', (m) => m.type() === 'error' && errs.push(m.text()));
  p.on('pageerror', (e) => errs.push(String(e)));
  await p.goto(url);
  await p.waitForSelector('.card');

  // The docs' fonts are embedded and actually loaded (the page may fetch nothing else).
  await p.evaluate(() => document.fonts.ready);
  assert.ok(await p.evaluate(() => document.fonts.check('16px Roboto') && document.fonts.check('16px "Roboto Mono"')), 'embedded fonts load');

  // The top says in one sentence whether anything is wrong, and the counts add up.
  assert.match(await p.textContent('.p-summary h1'), /\d+ of \d+ deploys? regressed|Nothing/);
  assert.ok(await p.$('.p-summary h1 b'), 'the last word of the sentence is bold');
  assert.equal(await p.$('.hero, .beam'), null, 'nothing decorative moves behind the data');
  await p.waitForFunction(() => [...document.querySelectorAll('.p-summary .counts b')].reduce((a, e) => a + Number(e.textContent), 0) === document.querySelectorAll('.card').length);
  assert.match(await p.textContent('.p-summary .origin'), /Recorded sample/);
  assert.match(await p.textContent('.p-state'), /Log source\s*flowing/);

  // Three boards, reachable by their tabs and by address.
  assert.deepEqual(await p.$$eval('.board-tabs .tab', (a) => a.map((e) => e.textContent)), ['Now', 'Case', 'Source health']);
  assert.equal(await p.getAttribute('.tab[href="#/now"]', 'aria-current'), 'page');

  // The theme switches, sticks across a reload, and the page keeps its black-and-white ground.
  const bg = () => p.evaluate(() => getComputedStyle(document.body).backgroundColor);
  const was = await p.evaluate(() => document.documentElement.dataset.theme);
  await p.click('#theme');
  const now = await p.evaluate(() => document.documentElement.dataset.theme);
  assert.notEqual(was, now);
  await p.waitForTimeout(500);
  assert.equal(await bg(), now === 'light' ? 'rgb(255, 255, 255)' : 'rgb(0, 0, 0)');
  await p.reload();
  await p.waitForSelector('.card');
  assert.equal(await p.evaluate(() => document.documentElement.dataset.theme), now, 'the choice is remembered');
  await p.click('#theme');

  // The first deploy opened is one that needs a person, with a canary-vs-stable chart.
  assert.match(await p.textContent('.detail-head h2'), /checkout|orders/);
  await p.waitForSelector('svg.chart .line.trt');
  assert.ok(await p.$('svg.chart .line.ctl'), 'a grade A deploy shows the stable pods line');

  // The chart fills its panel: its height follows the panel, not a fixed number.
  const fits = await p.evaluate(() => {
    const pl = document.querySelector('.p-canary .plot-wrap').getBoundingClientRect();
    const sv = document.querySelector('svg.chart').getBoundingClientRect();
    return Math.abs(pl.height - sv.height) < 3;
  });
  assert.ok(fits, 'chart is as tall as its panel');

  // Choosing another deploy changes the detail; a grade B one has no stable line.
  await p.click('.card:has-text("orders")');
  await p.waitForFunction(() => document.querySelector('.detail-head h2').textContent.includes('orders'));
  await p.waitForSelector('svg.chart .line.trt');
  assert.equal(await p.$('svg.chart .line.ctl'), null);

  // A deploy not judged yet says when it will be.
  await p.click('.card:has-text("search")');
  await p.waitForFunction(() => document.querySelector('.detail-head h2').textContent.includes('search'));
  assert.match(await p.textContent('.p-canary'), /waiting for the first judgement/i);

  // Hovering the chart shows a tooltip with values.
  await p.click('.card:has-text("checkout")');
  await p.waitForSelector('svg.chart .line.ctl');
  const box = await (await p.$('svg.chart')).boundingBox();
  await p.mouse.move(box.x + box.width * 0.4, box.y + 100);
  await p.waitForSelector('.tip:not([hidden])');
  assert.match(await p.textContent('.tip'), /Canary: [\d.]+ ms/);
  assert.match(await p.textContent('.tip'), /Stable pods: [\d.]+ ms/);

  // The same numbers are available as a table.
  await p.click('text=Show as table');
  await p.waitForSelector('.datatable table');
  assert.ok((await p.$$('.datatable tbody tr')).length > 10);
  // With the table open the panel scrolls; nothing is cut off, and the table can be scrolled into view whole.
  const fit = await p.evaluate(() => {
    const body = document.querySelector('.panel[data-panel="canary"] .p-body');
    body.scrollTop = body.scrollHeight;
    const t = document.querySelector('.datatable').getBoundingClientRect(), b = body.getBoundingClientRect();
    const plot = document.querySelector('.plot-wrap').getBoundingClientRect(), cap = document.querySelector('.p-canary .fig').getBoundingClientRect();
    return { tableInside: t.bottom <= b.bottom + 1, scrolls: body.scrollHeight > body.clientHeight, plotAboveCaption: plot.bottom <= cap.top + 1 };
  });
  assert.deepEqual(fit, { tableInside: true, scrolls: true, plotAboveCaption: true }, 'the table view is not cut off');

  // Picking a query in the evidence table changes the chart's query, and so does the list above the chart.
  const before = await p.textContent('.plot svg');
  await p.click('table.evidence tbody tr:nth-child(3) button.link');
  await p.waitForFunction((was) => document.querySelector('.plot svg').textContent !== was, before);
  const sel = await p.$eval('.unit-pick', (e) => e.value);
  const other = await p.$$eval('.unit-pick option', (o, s) => o.map((x) => x.value).find((v) => v !== s), sel);
  await p.selectOption('.unit-pick', other);
  await p.waitForFunction((v) => document.querySelector('.unit-pick').value === v, other);

  // Lists draw their own arrow, a fixed distance from the text.
  await p.waitForFunction(() => { const e = document.querySelector('.unit-pick'); return e && e.isConnected && getComputedStyle(e).paddingRight !== ''; });
  const selStyle = await p.evaluate(() => { const c = getComputedStyle(document.querySelector('.unit-pick')); return { app: c.appearance, pad: parseFloat(c.paddingRight), img: /data:image/.test(c.backgroundImage) }; });
  assert.equal(selStyle.img, true, 'a drawn arrow ' + JSON.stringify(selStyle));
  assert.ok(selStyle.pad >= 28, 'room for the arrow');

  // The grade key is there for anyone who does not know what A to D mean.
  await p.click('summary:has-text("What do the grades mean")');
  assert.match(await p.textContent('.key'), /Concurrent control/);

  // ---- time range ----------------------------------------------------------------
  await p.click('#range-btn');
  assert.equal(await p.getAttribute('#range-btn', 'aria-expanded'), 'true');
  await p.click('.preset:has-text("Last 15 minutes")');
  await p.waitForSelector('.empty:has-text("No deploys in this time range")');
  assert.match(await p.textContent('#range-btn'), /15 minutes/);
  // A custom range is checked before it is used.
  await p.click('#range-btn');
  await p.fill('#range-from', '2030-01-01T10:00');
  await p.fill('#range-to', '2030-01-01T11:00');
  await p.click('.custom button[type=submit]');
  assert.match(await p.textContent('.form-error'), /future/);
  await p.keyboard.press('Escape');
  await p.click('#range-btn');
  await p.click('.preset:has-text("Last 6 hours")');
  await p.waitForSelector('.card');
  // The range is remembered per board, across a reload.
  await p.click('#range-btn');
  await p.click('.preset:has-text("Last 7 days")');
  await p.waitForSelector('.card');
  await p.reload();
  await p.waitForSelector('.card');
  assert.match(await p.textContent('#range-btn'), /7 days/);
  await p.click('#range-btn');
  await p.click('.preset:has-text("Last 6 hours")');
  await p.waitForSelector('.card');

  // Keyboard: cards are buttons.
  await p.focus('.card');
  await p.keyboard.press('Enter');

  // ---- the case board --------------------------------------------------------------
  await p.click('.card:has-text("checkout")');
  await p.click('.open-case');
  await p.waitForSelector(panel('ratios') + ' svg.ratio');
  assert.match(p.url(), /#\/case\/payments%2Fcheckout/);
  assert.equal(await p.$('.open-case'), null, 'the case board does not link to itself');
  assert.ok((await p.$$('svg.ratio .rrow')).length >= 2, 'one row per query');
  assert.ok(await p.$('svg.ratio .one'), 'the line at 1.0 is drawn');
  assert.match(await p.textContent(panel('evaluations')), /#1[\s\S]*#2/);
  assert.match(await p.textContent(panel('evaluations') + ' .evdiff-head'), /#1 → #2/);
  // Reloading on a case address opens that case.
  await p.reload();
  await p.waitForSelector('svg.ratio .rrow');
  assert.match(await p.textContent('.detail-head h2'), /checkout/);

  // ---- source health and safety ------------------------------------------------------
  await p.click('.tab:has-text("Source health")');
  await p.waitForSelector('table.sources');
  assert.match(await p.textContent('table.sources'), /Query statistics[\s\S]*Postgres log/);
  assert.match(await p.textContent(panel('safety')), /May act/);
  await p.click('.tab:has-text("Now")');
  await p.waitForSelector('.card');

  // ---- the info button ---------------------------------------------------------------------
  const nPanels = (await p.$$('.panel')).length;
  assert.equal((await p.$$('.panel .p-info')).length, nPanels, 'every panel has an info button');
  const info = panel('evidence') + ' .p-info';
  assert.match(await p.getAttribute(info, 'aria-label'), /^About Evidence$/);
  await p.hover(info);
  await p.waitForSelector('.infotip:not([hidden])');
  assert.match(await p.textContent('.infotip'), /Each query the radar tested/);
  assert.equal(await p.getAttribute(info, 'aria-describedby'), 'infotip');
  const tb = await (await p.$('.infotip')).boundingBox();
  const pb = await (await p.$(panel('evidence'))).boundingBox();
  const vp = p.viewportSize();
  assert.ok(tb.x >= 0 && tb.y >= 0 && tb.x + tb.width <= vp.width && tb.y + tb.height <= vp.height, 'the tip stays on screen');
  await p.mouse.move(4, 4);
  await p.waitForSelector('.infotip', { state: 'hidden' });
  await p.click(info); // a click pins it
  await p.mouse.move(4, 4);
  await p.waitForTimeout(250);
  assert.equal(await p.isVisible('.infotip'), true, 'a pinned tip stays');
  assert.equal(await p.getAttribute(info, 'aria-expanded'), 'true');
  await p.keyboard.press('Escape');
  await p.waitForSelector('.infotip', { state: 'hidden' });
  assert.equal(await p.getAttribute(info, 'aria-expanded'), 'false');
  await p.click(panel('summary') + ' .p-info');
  await p.waitForSelector('.infotip:not([hidden])');
  {
    // Where the screen allows, the tip stays off the panel it describes and has square corners.
    const t = await (await p.$('.infotip')).boundingBox(), sp = await (await p.$(panel('summary'))).boundingBox();
    assert.ok(t.y >= sp.y + sp.height - 1 || t.y + t.height <= sp.y + 1, 'the tip does not cover its own panel');
    assert.equal(await p.$eval('.infotip', (e) => getComputedStyle(e).borderTopLeftRadius), '0px');
  }
  assert.match(await p.textContent('.infotip'), /is anything wrong/);
  await p.mouse.click(4, 4); // a click elsewhere closes a pinned tip
  await p.waitForSelector('.infotip', { state: 'hidden' });
  // The summary's counts and its sentence agree: "regressed" in the sentence is the sum of the two regressed kinds.
  const legend = await p.textContent(panel('summary') + ' .counts');
  assert.match(legend, /regressed, no action/);
  assert.match(legend, /regressed, acted on/);


  // What the command keeps between runs (its data directory), as the page sees it.
  const savedProfile = async () => { await p.waitForTimeout(700); return p.evaluate(async () => (await (await fetch('/dashboard/profile')).json()).profile); };
  const wipeProfile = async () => {
    await p.waitForTimeout(700); // let a pending save land first
    await p.evaluate(async () => { localStorage.clear(); await fetch('/dashboard/profile', { method: 'DELETE', headers: { 'X-Requested-By': 'pgrr-dashboard' } }); });
  };

  // ---- the focus view ----------------------------------------------------------------------
  const focusOpen = () => p.waitForSelector('#focus:not([hidden]) .focus-panel .p-body > *');
  const focusShut = () => p.waitForSelector('#focus', { state: 'hidden' });
  const hash = () => p.evaluate(() => location.hash);
  const field = (label) => p.locator(`#focus .field:not(.na):has(.field-label:text-is("${label}"))`);

  // A click on a panel's own text opens it large; its buttons and the info button do not.
  await p.click(panel('canary') + ' .fig');
  await focusOpen();
  assert.equal(await hash(), '#/now?view=canary', 'the address says which panel is in focus');
  assert.equal(await p.isVisible('#board'), false, 'only the one panel is shown');
  assert.match(await p.textContent('.focus-title'), /Canary/);
  assert.equal(await p.getAttribute('#focus-pane', 'aria-label'), 'Settings for Canary vs control');
  assert.ok(await p.$('#focus aside.focus-pane'), 'the settings are an aside landmark');
  assert.equal(await p.evaluate(() => document.activeElement.className), 'focus-title', 'focus moves into the view');
  assert.ok(await p.$('#focus svg.chart .line.trt'), 'the chart is drawn large');
  // Square corners, like the info tip.
  assert.equal(await p.$eval('#focus .viz-tile', (e) => getComputedStyle(e).borderRadius), '0px');
  assert.equal(await p.$eval('#focus-pane', (e) => getComputedStyle(e).borderRadius), '0px');
  await p.keyboard.press('Escape');
  await focusShut();
  assert.doesNotMatch(await hash(), /view=/, 'Escape leaves the view and the address goes back');
  assert.equal(await p.isVisible('#board'), true);

  await p.click(panel('canary') + ' .p-info');
  assert.equal(await p.isVisible('#focus'), false, 'the info button does not open the panel');
  await p.keyboard.press('Escape');
  await p.click(panel('deploys') + ' .card >> nth=0');
  assert.equal(await p.isVisible('#focus'), false, 'a deploy card selects; it does not open the panel');
  await p.selectOption(panel('canary') + ' select.unit-pick', { index: 1 }).catch(() => {});
  assert.equal(await p.isVisible('#focus'), false, 'a list inside a panel does not open it');

  // The expand button and the V key do the same, and Back undoes the step.
  await p.hover(panel('evidence') + ' .p-body');
  await p.click(panel('evidence') + ' .p-expand');
  await focusOpen();
  assert.equal(await hash(), '#/now?view=evidence');
  await p.click('.focus-back');
  await focusShut();
  assert.doesNotMatch(await hash(), /view=/);
  await p.hover(panel('ledger') + ' .p-body');
  await p.keyboard.press('v');
  await focusOpen();
  assert.equal(await hash(), '#/now?view=ledger', 'V opens the panel under the pointer');
  await p.keyboard.press('Escape');
  await focusShut();

  // The address is a deep link; an unknown panel is dropped from it.
  await p.goto(url + '#/now?view=evidence');
  await p.reload();
  await focusOpen();
  assert.match(await p.textContent('.focus-title'), /Evidence/);
  await p.goto(url + '#/now?view=nothing-here');
  await p.reload();
  await p.waitForSelector('.card');
  assert.equal(await p.isVisible('#focus'), false);
  assert.equal(await hash(), '#/now', 'an unknown panel is dropped from the address');

  // Settings apply at once, are kept, and can be discarded.
  await p.click(panel('deploys') + ' .ph');
  await focusOpen();
  assert.equal(await p.isDisabled('#focus button:text-is("Discard changes")'), true, 'nothing to discard yet');
  const nCards = await p.$$eval('#focus .card', (e) => e.length);
  assert.ok(nCards >= 3);
  await p.locator('#focus .viz-opt:has-text("Compact list") input').check({ force: true });
  await p.waitForSelector('#focus .p-cards.as-list .card.row');
  assert.equal(await p.$$eval('#focus .card.row', (e) => e.length), nCards, 'the same deploys, drawn as a list');
  await field('Most deploys to show').locator('input').fill('2');
  await p.waitForFunction(() => document.querySelectorAll('#focus .card').length === 2);
  assert.match(await p.textContent('#focus .ph .count'), /2 of \d+/);
  assert.equal(await p.isDisabled('#focus button:text-is("Discard changes")'), false);
  await field('Order').locator('select').selectOption('worst');
  assert.match(await p.textContent('#focus .card.row .row-state'), /Acted|Regress/, 'worst first puts a problem on top');
  assert.ok(await p.$('#focus .field.modified'), 'a changed setting is marked');
  await p.fill('#focus .pane-search', 'order');
  assert.deepEqual(await p.$$eval('#focus .field:not([hidden]):not(.na) .field-label', (e) => e.map((x) => x.textContent)), ['Order'], 'search narrows the settings');
  await p.fill('#focus .pane-search', '');
  assert.ok((await savedProfile()).layout, 'a change is kept in the data directory like any edit of the layout');
  await p.click('#focus button:text-is("Discard changes")');
  await p.waitForSelector('#focus .p-cards:not(.as-list) .card');
  assert.equal(await p.$$eval('#focus .card', (e) => e.length), nCards, 'discarding goes back to how it was');
  assert.equal(((await savedProfile()) || {}).layout, undefined, 'and no layout edit is left over');

  // A change made in the view is there on the board afterwards, and in the exported file.
  await p.locator('#focus .viz-opt:has-text("Compact list") input').check({ force: true });
  assert.equal(await p.$eval('#focus details[data-group="Panel"]', (e) => e.open), false, 'the panel name and description are tucked away until wanted');
  await p.click('#focus details[data-group="Panel"] summary');
  await field('Title').locator('input').fill('Every deploy');
  await p.waitForFunction(() => document.querySelector('#focus .ph').textContent.startsWith('Every deploy'));
  await p.keyboard.press('Escape');
  await p.keyboard.press('Escape');
  await focusShut();
  assert.ok(await p.$(panel('deploys') + ' .p-cards.as-list'), 'the board shows the panel as it was set');
  assert.match(await p.textContent(panel('deploys') + ' .ph'), /Every deploy/);
  await p.click('#edit-btn');
  await p.click('#export-yaml');
  const exported = await p.inputValue('#export textarea');
  assert.match(exported, /title: Every deploy/);
  assert.match(exported, /viz: list/);
  await p.keyboard.press('Escape');
  await p.click('#reset-layout');
  await p.click('#edit-done');
  assert.ok(await p.$(panel('deploys') + ' .p-cards:not(.as-list)'), 'undoing the edits puts the panel back');

  // Options that belong to one way of drawing the panel appear only with it.
  await p.goto(url + '#/now?view=evidence');
  await p.reload();
  await focusOpen();
  assert.ok(await field('Show the flags').count(), 'the table has columns to choose');
  await p.locator('#focus .viz-opt:has-text("Ratio chart") input').check({ force: true });
  await p.waitForSelector('#focus svg.ratio');
  assert.equal(await field('Show the flags').count(), 0, 'the ratio chart has none');
  await p.click('#focus button:text-is("Reset to defaults")');
  await p.waitForSelector('#focus table.evidence');
  assert.equal(await p.$$eval('#focus .field.modified', (e) => e.length), 0);

  // Filters and order change what the table lists.
  await field('Show').locator('select').selectOption('flagged');
  await p.waitForFunction(() => { const rows = [...document.querySelectorAll('#focus table.evidence tbody tr')]; return rows.length > 0 && rows.every((r) => r.querySelector('.ugrade')); });
  await field('Order').locator('select').selectOption('name');
  const names = await p.$$eval('#focus table.evidence tbody tr td:first-child .link', (e) => e.map((x) => x.textContent));
  assert.deepEqual(names, [...names].sort((a, b) => a.localeCompare(b)), 'ordered by name');
  await field('Show the flags').locator('input').uncheck({ force: true });
  assert.equal(await p.$$eval('#focus table.evidence th', (e) => e.map((x) => x.textContent).includes('Flags')), false, 'a column that was switched off is gone');
  await p.click('#focus button:text-is("Reset to defaults")');

  // The chart's settings: what it carries, and the table instead of the picture.
  await p.goto(url + '#/now?view=canary');
  await p.reload();
  await focusOpen();
  assert.ok(await p.$('#focus .nbar'));
  await field('Show how many calls each point rests on').locator('input').uncheck({ force: true });
  await p.waitForFunction(() => !document.querySelector('#focus .nbar'));
  await field('Show the level before the deploy').locator('input').uncheck({ force: true });
  await p.waitForFunction(() => !document.querySelector('#focus svg.chart .baseline'));
  await field('Start the axis at zero').locator('input').uncheck({ force: true });
  await p.waitForFunction(() => { const t = [...document.querySelectorAll('#focus svg.chart .tick')].map((e) => e.textContent).filter((x) => /^\d+$/.test(x)); return t.length && Number(t[0]) > 0; });
  await p.locator('#focus .viz-opt:has-text("Table") input').check({ force: true });
  await p.waitForSelector('#focus .datatable.tall tbody tr');
  assert.equal(await p.$('#focus svg.chart'), null, 'the table replaces the picture');
  assert.equal(await field('Start the axis at zero').count(), 0, 'and the picture\'s settings are not offered');
  await p.click('#focus button:text-is("Reset to defaults")');
  await p.waitForSelector('#focus svg.chart .nbar');

  // Switches on a panel that only shows numbers.
  await p.goto(url + '#/now?view=summary');
  await p.reload();
  await focusOpen();
  await field('Show the bar').locator('input').uncheck({ force: true });
  await p.waitForFunction(() => document.querySelector('#focus .segbar').hidden);
  assert.ok(await p.isVisible('#focus .counts'), 'the counts stay');
  await p.click('#focus button:text-is("Reset to defaults")');
  await p.goto(url + '#/now?view=figures');
  await p.reload();
  await focusOpen();
  await field('Show the evidence grade').locator('input').uncheck({ force: true });
  assert.equal(await p.$$eval('#focus .stat', (e) => e.length), 4);
  await p.click('#focus button:text-is("Reset to defaults")');
  await p.goto(url + '#/now?view=evidence');
  await p.reload();
  await focusOpen();

  // A panel that follows the picked deploy has the deploy picker beside it.
  assert.equal(await p.isVisible('#focus-deploy'), true);
  const ids = await p.$$eval('#focus-deploy option', (o) => o.map((x) => x.value));
  const currentId = await p.inputValue('#focus-deploy');
  const another = ids.find((v) => v !== currentId);
  await p.selectOption('#focus-deploy', another);
  await p.waitForFunction((v) => document.querySelector('#focus-deploy').value === v, another);
  await p.keyboard.press('Escape');
  await p.keyboard.press('Escape');
  await focusShut();
  assert.equal(await p.getAttribute(`.card[aria-pressed="true"]`, 'aria-pressed'), 'true', 'a deploy picked in the view stays picked');
  await p.goto(url + '#/now?view=deploys');
  await p.reload();
  await focusOpen();
  assert.equal(await p.isVisible('#focus-deploy'), false, 'the deploy list itself needs no picker');
  await p.click('.focus-back');

  // The settings can be hidden to give the panel the whole width.
  await p.goto(url + '#/now?view=canary');
  await p.reload();
  await focusOpen();
  const wide = await p.$eval('#focus .focus-panel', (e) => e.getBoundingClientRect().width);
  await p.click('#focus button:text-is("Hide settings")');
  assert.ok((await p.$eval('#focus .focus-panel', (e) => e.getBoundingClientRect().width)) > wide + 300);
  await p.click('#focus button:text-is("Show settings")');
  await p.goto(url);
  await p.waitForSelector('.card');
  await wipeProfile();


  // ---- the query under a panel in focus -----------------------------------------------------
  await p.goto(url + '#/now?view=deploys');
  await p.reload();
  await focusOpen();
  const qin = '#focus .qinput';
  const qstatus = () => p.textContent('#focus .qstatus');
  assert.equal(await p.getAttribute(qin, 'role'), 'combobox');
  await p.waitForSelector('#focus .card-li');
  const cardsBefore = await p.$$eval('#focus .card-li', (e) => e.length);
  // Focusing the field lists what can be filtered on, with each field explained.
  await p.click(qin);
  const fieldsShown = await p.$$eval('#focus .qopt .qopt-label', (e) => e.map((x) => x.textContent));
  assert.ok(fieldsShown.includes('app') && fieldsShown.includes('state') && fieldsShown.includes('grade'), 'the fields are offered');
  // After a field, the values that exist right now are offered, with how many each matches.
  await p.keyboard.type('app:');
  const values = await p.$$eval('#focus .qopt', (e) => e.map((x) => x.textContent));
  assert.ok(values.length > 1 && values.some((v) => /\d/.test(v)), 'values come from the live data, with counts');
  await p.keyboard.press('ArrowDown');
  await p.keyboard.press('Enter');
  await p.waitForTimeout(450);
  assert.match(await p.inputValue(qin), /^app:\S+/, 'an accepted suggestion is written into the field');
  assert.match(await qstatus(), /^Shows /, 'the field says what it now shows');
  const cardsAfter = await p.$$eval('#focus .card-li', (e) => e.length);
  assert.ok(cardsAfter < cardsBefore, `the panel is filtered (${cardsBefore} -> ${cardsAfter})`);
  // A query that cannot run says why and leaves the panel as it was.
  await p.fill(qin, 'app:');
  await p.waitForTimeout(450);
  assert.ok(await p.$('#focus .qinput.bad, #focus .qstatus.bad'), 'an incomplete query is flagged');
  assert.equal(await p.$$eval('#focus .card-li', (e) => e.length), cardsAfter, 'and not applied');
  await p.fill(qin, 'nonsense:1');
  await p.waitForTimeout(450);
  assert.match(await qstatus(), /nonsense/i, 'an unknown field is named');
  // Escape closes the suggestions first and only then leaves the view.
  await p.fill(qin, 'sta');
  await p.keyboard.press('Escape');
  assert.equal(await p.isVisible('#focus .qlist'), false);
  assert.equal(await p.isVisible('#focus'), true, 'the first Escape only closes the list');
  // A valid query is kept: it survives a reload and shows on the board.
  await p.fill(qin, 'state:regressed');
  await p.keyboard.press('Enter');
  await p.waitForTimeout(450);
  await p.reload();
  await focusOpen();
  assert.equal(await p.inputValue(qin), 'state:regressed', 'the query is saved with the layout');
  // It is on disk, not in this browser: a browser that has never seen the dashboard finds it too.
  const fresh = await (await b.newContext({ viewport: { width: 1440, height: 900 } })).newPage();
  await fresh.goto(url + '#/now?view=deploys');
  await fresh.waitForSelector('#focus .qinput');
  assert.equal(await fresh.inputValue('#focus .qinput'), 'state:regressed', 'a new browser opens with the saved query');
  assert.deepEqual(await fresh.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('pgrr-layout'))), [], 'nothing about it is kept in the browser');
  await fresh.context().close();
  // Clear puts the panel back.
  await p.click('#focus .qclear');
  await p.waitForTimeout(450);
  assert.equal(await p.inputValue(qin), '');
  assert.equal(await p.$$eval('#focus .card-li', (e) => e.length), cardsBefore, 'clearing restores the panel');
  // Panels with nothing to filter have no query field.
  await p.goto(url + '#/health?view=sources');
  await p.reload();
  await focusOpen();
  assert.equal(await p.$('#focus .qinput'), null, 'no query field where there is nothing to filter');
  await p.goto(url);
  await p.waitForSelector('.card');
  await wipeProfile();

  // ---- editing the layout ----------------------------------------------------------------
  const at = (id) => p.$eval(panel(id), (e) => ['--x', '--y', '--w', '--h'].map((k) => Number(e.style.getPropertyValue(k))));
  assert.equal(await p.isVisible('#editbar'), false);
  await p.click('#edit-btn');
  assert.equal(await p.isVisible('#editbar'), true);
  assert.equal(await p.getAttribute('#edit-btn', 'aria-pressed'), 'true');
  assert.ok(await p.$('.grid.editing'));
  assert.equal(await p.isVisible(panel('figures') + ' .p-info'), false, 'the info button gives way to the edit bar');

  // Dragging a panel by its bar moves it on the grid, and nothing ends up overlapping.
  const g = await (await p.$('#board')).boundingBox();
  const colW = g.width / 12;
  const w0 = await at('figures');
  const grip = await (await p.$(panel('figures') + ' .p-move')).boundingBox();
  await p.mouse.move(grip.x + 30, grip.y + 10);
  await p.mouse.down();
  await p.mouse.move(grip.x + 30 - colW * 6, grip.y + 10 + 40 * 2, { steps: 8 });
  await p.mouse.up();
  const w1 = await at('figures');
  assert.notDeepEqual(w1, w0, 'the panel moved');
  const rects = await p.$$eval('.panel', (els) => els.map((e) => e.getBoundingClientRect()).map((r) => [r.left, r.top, r.right, r.bottom]));
  for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length; j++) {
    const [a, c] = [rects[i], rects[j]];
    assert.ok(!(a[0] < c[2] - 1 && c[0] < a[2] - 1 && a[1] < c[3] - 1 && c[1] < a[3] - 1), `panels ${i} and ${j} overlap`);
  }

  // Resizing from the corner changes its size, and never goes under the type's minimum.
  await p.$eval(panel('why'), (e) => e.scrollIntoView({ block: 'center' }));
  const corner = await (await p.$(panel('why') + ' .p-resize')).boundingBox();
  await p.mouse.move(corner.x + 10, corner.y + 10);
  await p.mouse.down();
  await p.mouse.move(corner.x + 10 - colW * 8, corner.y + 10 - 40 * 6, { steps: 6 });
  await p.mouse.up();
  const why = await at('why');
  assert.deepEqual(why.slice(2), [4, 3], 'shrunk to the minimum of a why panel, not below');

  // The keyboard does the same: arrows move, Shift and arrows resize.
  await p.$eval(panel('summary'), (e) => e.scrollIntoView({ block: 'start' }));
  await p.focus(panel('summary') + ' .p-move');
  const s0 = await at('summary');
  await p.keyboard.press('Shift+ArrowDown');
  assert.equal((await at('summary'))[3], s0[3] + 1);

  // Hiding a panel closes the gap; adding one puts it at the bottom.
  const n0 = (await p.$$('.panel')).length;
  await p.click(panel('ledger') + ' .p-x');
  assert.equal((await p.$$('.panel')).length, n0 - 1);
  await p.selectOption('#add-panel', 'safety');
  await p.waitForSelector(panel('safety'));
  assert.equal((await p.$$('.panel')).length, n0);

  // The edits stay across a reload, and Undo brings the layout back.
  await p.reload();
  await p.waitForSelector('.card');
  assert.equal(await p.$(panel('ledger')), null, 'the hidden panel is still hidden');
  assert.ok(await p.$(panel('safety')));
  await p.click('#edit-btn');
  await p.click('#export-yaml');
  await p.waitForSelector('dialog[open] textarea');
  const yaml = await p.inputValue('dialog textarea');
  assert.match(yaml, /^apiVersion: pgrr.io\/v1\nkind: Dashboard\nboards:\n  - id: now/);
  assert.match(yaml, /type: safety/);
  assert.doesNotMatch(yaml, /id: ledger\n {8}type: ledger[\s\S]*- id: case/, 'the hidden panel is not in the file');
  await p.click('dialog button:has-text("Close")');
  await p.click('#reset-layout');
  await p.waitForSelector(panel('ledger'));
  assert.equal(await p.$(panel('safety')), null, 'the added panel is gone');
  assert.equal((await p.$$('.panel')).length, n0, 'back to the default panels');
  await p.click('#edit-done');
  assert.equal(await p.isVisible('#editbar'), false);

  // An empty range explains itself even in edit mode's absence; reduced motion shows everything at once.
  const calm = await (await b.newContext({ viewport: { width: 1440, height: 900 }, reducedMotion: 'reduce' })).newPage();
  calm.on('console', (m) => m.type() === 'error' && errs.push(m.text()));
  await calm.goto(url);
  await calm.waitForSelector('svg.chart .line.trt');
  const opacities = await calm.$$eval('.seg, .gbar i, .card-li, .stat .num, .panel', (els) => els.map((e) => [getComputedStyle(e).opacity, getComputedStyle(e).transform]));
  assert.ok(opacities.length > 10);
  for (const [o, t] of opacities) { assert.equal(o, '1'); assert.ok(t === 'none' || t === 'matrix(1, 0, 0, 1, 0, 0)', `no leftover transform: ${t}`); }
  assert.equal(await calm.$eval('svg.chart .line.trt', (e) => getComputedStyle(e).strokeDashoffset), '0px', 'the chart is drawn, not mid-animation');

  // A narrow screen stacks the panels in reading order, with no sideways scroll.
  const narrow = await (await b.newContext({ viewport: { width: 390, height: 800 }, deviceScaleFactor: 1 })).newPage();
  narrow.on('console', (m) => m.type() === 'error' && errs.push(m.text()));
  await narrow.goto(url);
  await narrow.waitForSelector('.card');
  await narrow.waitForSelector('svg.chart');
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'no horizontal scroll at 390px');
  assert.equal(await narrow.isVisible('#edit-btn'), false, 'layout editing needs room');
  const tops = await narrow.$$eval('.panel', (els) => els.map((e) => e.getBoundingClientRect().top));
  assert.deepEqual(tops, [...tops].sort((x, y) => x - y), 'panels stack top to bottom');


  // The focus view stacks on a narrow screen: the panel, then its settings, with no sideways scroll.
  await narrow.goto(url + '#/now?view=canary');
  await narrow.reload();
  await narrow.waitForSelector('#focus:not([hidden]) svg.chart');
  assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, 'no horizontal scroll in the focus view at 390px');
  await narrow.waitForTimeout(500); // the view slides in; measure it at rest
  const [stageBox, paneBox] = await narrow.evaluate(() => ['.focus-panel', '.focus-pane'].map((q) => { const r = document.querySelector(q).getBoundingClientRect(); return [r.top, r.bottom]; }));
  assert.ok(paneBox[0] >= stageBox[1] - 1, 'the settings sit under the panel');
  await narrow.goto(url);

  assert.deepEqual(errs, [], 'no console errors (a blocked font or script is one)');
  if (shots) {
    const fs = require('node:fs');
    fs.mkdirSync(shots, { recursive: true });
    for (const [name, hash] of [['now', '#/now'], ['case', '#/case'], ['health', '#/health']]) {
      await p.goto(url + hash);
      await p.waitForSelector('.panel');
      await p.waitForTimeout(2200);
      await p.screenshot({ path: `${shots}/${name}-${await p.evaluate(() => document.documentElement.dataset.theme)}.png`, fullPage: true });
    }
    await narrow.screenshot({ path: `${shots}/narrow.png`, fullPage: true });
  }
  await b.close();
  console.log('dashboard smoke test: ok');
})().catch((e) => { console.error(e); process.exit(1); });
