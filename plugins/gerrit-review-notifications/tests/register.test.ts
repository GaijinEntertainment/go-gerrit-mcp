import {expect, mock, test} from 'claude-code/testing';
import type {Engine} from 'claude-code/testing';
import type {On} from 'claude-code';

const SUBSCRIBE = 'mcp__gerrit-review-notifications__subscribe_change' as const;
const UNSUBSCRIBE = 'mcp__gerrit-review-notifications__unsubscribe_change' as const;
const POLL_INTERVAL_MS = 60_000;

const URL_123 = 'https://gerrit.example.com/c/123';
const START = {change: 123, url: URL_123, since: '2026-07-01T10:00:00Z', status: 'NEW'};
const POINT = {change: 123, since: START.since, status: START.status};
const ACTIVITY = {
	change: 123,
	since: '2026-07-02T11:30:00Z',
	status: 'NEW',
	summary: '1 message',
	content: '<review_activity change="123"><message author="bob">please rename the helper</message></review_activity>',
};
const ANNOUNCEMENT = 'Gerrit review activity, in full in the note from this plugin:';
const PROMPT = `${ANNOUNCEMENT}\n123 · 1 message`;

const READY = {name: 'go-gerrit-mcp', version: 'v1.2.0', state: 'ready', tools: ['get_review_activity']};
const REJECTED = {name: 'go-gerrit-mcp', version: 'v1.2.0', state: 'credentials_rejected', reason: 'status=401'};
const FAILED = 'fail';

type ActivityCall = {changes: {change: number; since?: string; status?: string}[]};

const failed = (text: string) => ({value: {content: [{type: 'text', text}], isError: true}});

// Stands for the engine and the Gerrit MCP server beneath the plugin: answers
// get_server_info with `info` (no such tool when null) and each
// get_review_activity call with the next of `answers` (an error for FAILED),
// and records the calls and the submitted prompts.
const world = (
	on: On,
	answers: (object[] | typeof FAILED)[],
	whileServerAnswers?: (call: number) => Promise<unknown>,
	info: object | null = READY,
) => {
	const calls: ActivityCall[] = [];
	const servers: string[] = [];
	const prompts: string[] = [];
	const server: {info: object | null} = {info};

	on('session.start', (_, e) => ({cwd: e.cwd}));
	on('tool.register', (_, e) => ({value: {tool: `mcp__gerrit-review-notifications__${e.name}`}}));
	on('mcp.connect', (_, e) => ({value: {isConnected: true, server: e.server}}));
	on('mcp.call', async (_, e) => {
		servers.push(e.server);

		if (e.tool === 'get_server_info') {
			return server.info === null
				? failed('unknown tool "get_server_info"')
				: {value: {content: [{type: 'text', text: JSON.stringify(server.info)}], isError: false}};
		}

		calls.push(e.args as ActivityCall);

		const answer = answers.shift();

		if (answer === FAILED) {
			return failed('unknown tool "get_review_activity"');
		}

		const text = JSON.stringify({changes: answer});

		await whileServerAnswers?.(calls.length);

		return {value: {content: [{type: 'text', text}], isError: false}};
	});
	on('prompt.submit', (_, e) => {
		prompts.push(e.text);

		return {text: e.text};
	});
	on('turn.start', (_, e) => ({turnId: e.turnId}));
	on('turn.complete', () => ({text: ''}));

	return {calls, servers, prompts, server, clock: mock.clock(on), session: mock.session(on)};
};

const run = ($: Engine, tool: typeof SUBSCRIBE | typeof UNSUBSCRIBE, input: {change: number}) =>
	$.tool.run({tool, tool_use_id: 'call', ...input});

const start = ($: Engine) => $.session.start({cwd: '/', surface: 'terminal', isInteractive: true});

const BAND = {
	plugin: 'gerrit-review-notifications',
	component: 'AbovePrompt',
	requestId: 'band',
	surface: 'terminal',
	props: {
		hasSurvey: false,
		isCollapsed: false,
		isWorking: false,
		maxRows: 10,
		bodyColumns: 80,
		scroll: {offset: 0, bodyRows: 10},
		view: {},
	},
} as const;

// The text of the band after "Gerrit", and the targets of its links.
const band = async ($: Engine) => {
	const ui = await $.ui.mount(BAND);
	const found = await ui.find({type: 'Text', text: /^ · /});
	const links = await ui.findAll({type: 'Link'});

	await ui.unmount();

	return {text: found?.text.trim(), hrefs: links.map((link) => link.props.href)};
};

test('activity for an idle session is submitted as a prompt, and the next poll asks past it', async ($, on) => {
	const {calls, prompts, clock, session} = world(on, [[START], [ACTIVITY], [ACTIVITY]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts[0]).toBe(PROMPT);
	expect(session.appended()[0]?.message.content).toEqual([{type: 'text', text: ACTIVITY.content}]);
	expect(calls[0]).toEqual({changes: [{change: 123}]});
	expect(calls[1]).toEqual({changes: [POINT]});
	expect(calls[2]).toEqual({changes: [{change: 123, since: ACTIVITY.since, status: 'NEW'}]});
});

test('activity during a running main turn is appended to the conversation', async ($, on) => {
	const {prompts, clock, session} = world(on, [[START], [ACTIVITY]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await $.turn.start({text: 'work', turnId: 't1'});
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([]);
	expect(session.appended().map((row) => row.message.content)).toEqual([[{type: 'text', text: ACTIVITY.content}]]);
});

test('a subagent turn does not count as a running main turn', async ($, on) => {
	const {prompts, clock} = world(on, [[START], [ACTIVITY]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await $.turn.start({text: 'work', turnId: 't1', agentId: 'a1'});
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([PROMPT]);
});

test('an ended change is delivered once and then no longer asked about', async ($, on) => {
	const {calls, prompts, clock} = world(on, [[START], [{...ACTIVITY, status: 'MERGED', ended: true}]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);
	await clock.advance(POLL_INTERVAL_MS);
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([`${PROMPT}, subscription ended`]);
	expect(calls.length).toBe(2);
});

test('activity on several changes in one poll is one delivery', async ($, on) => {
	const other = {
		...ACTIVITY,
		change: 456,
		summary: 'Code-Review +2, NEW -> MERGED',
		content: '<review_activity change="456" status="MERGED"><transition from="NEW" to="MERGED"/></review_activity>',
	};
	const {calls, prompts, clock, session} = world(on, [[START], [{...START, change: 456}], [ACTIVITY, other]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await run($, SUBSCRIBE, {change: 456});
	await clock.advance(POLL_INTERVAL_MS);

	expect(calls[2]).toEqual({changes: [POINT, {...POINT, change: 456}]});
	expect(prompts).toEqual([`${PROMPT}\n456 · Code-Review +2, NEW -> MERGED`]);
	expect(session.appended().length).toBe(1);
});

test('the server option names the MCP server that is asked', {options: {server: 'gerrit-work'}}, async ($, on) => {
	const {servers} = world(on, [[START]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});

	expect(servers).toEqual(['gerrit-work', 'gerrit-work']);
});

test('a change that the server did not read keeps its place', async ($, on) => {
	const {calls, clock} = world(on, [[START], [{...START, error: 'bad gateway'}], [ACTIVITY]]);

	on('ui.log', () => ({value: undefined}));

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);
	await clock.advance(POLL_INTERVAL_MS);

	expect(calls[2]).toEqual({changes: [POINT]});
});

test('a merged change is refused at subscribe', async ($, on) => {
	const {calls, clock} = world(on, [[{change: 123, since: START.since, status: 'MERGED', ended: true}]]);

	await start($);
	const ran = await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);

	expect(ran.deny).toBe('change 123 is MERGED; nothing is left to notify about');
	expect(calls.length).toBe(1);
});

test('a change outside the project scope is refused with the reason from the server', async ($, on) => {
	const reason = 'the change is outside the configured project scope';
	world(on, [[{change: 123, url: URL_123, ended: true, summary: reason}]]);

	await start($);
	const ran = await run($, SUBSCRIBE, {change: 123});

	expect(ran.deny).toBe(`change 123 was not read: ${reason}`);
});

test('a second subscribe to the same change is refused', async ($, on) => {
	world(on, [[START]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	const ran = await run($, SUBSCRIBE, {change: 123});

	expect(ran.deny).toBe('change 123 is already subscribed in this session');
});

test('a main turn that started before a reload still counts as running', async ($, on) => {
	const {prompts, clock, session} = world(on, [[START], [ACTIVITY]]);

	// What the code before the reload stored: the reload never heard turn.start.
	on('state.get', (_, e, next) => (e.key === 'isMainTurnRunning' ? {value: {value: true, version: 1}} : next(e)));

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([]);
	expect(session.appended().length).toBe(1);
});

test('a completed main turn makes the session idle again', async ($, on) => {
	const {prompts, clock} = world(on, [[START], [ACTIVITY]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await $.turn.start({text: 'work', turnId: 't1'});
	await $.turn.complete({answer: 'done', durationMs: 1, isAborted: false, turnId: 't1', reason: 'answer'});
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([PROMPT]);
});

test('the band above the prompt names the subscribed changes', async ($, on) => {
	const url456 = 'https://gerrit.example.com/c/456';
	world(on, [[START], [{...START, change: 456, url: url456}]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await run($, SUBSCRIBE, {change: 456});

	expect(await band($)).toEqual({text: '· subscribed to 2 changes:', hrefs: [URL_123, url456]});

	await run($, UNSUBSCRIBE, {change: 456});

	expect(await band($)).toEqual({text: '· subscribed to change', hrefs: [URL_123]});
});

test('a server without get_server_info is refused at subscribe, and the band says so', async ($, on) => {
	const {calls} = world(on, [[START]], undefined, null);

	await start($);
	const ran = await run($, SUBSCRIBE, {change: 123});
	const problem =
		'MCP server "gerrit" did not answer get_server_info ([{"type":"text","text":"unknown tool \\"get_server_info\\""}]): ' +
		'it is not go-gerrit-mcp, or a go-gerrit-mcp older than this plugin that needs an update';

	expect(ran.deny).toBe(`change 123 was not subscribed: ${problem}`);
	expect(calls).toEqual([]);
	expect(await band($)).toEqual({text: `· ${problem}`, hrefs: []});
});

test('a server that is not ready is refused at subscribe with its reason', async ($, on) => {
	const {calls} = world(on, [[START]], undefined, REJECTED);

	await start($);
	const ran = await run($, SUBSCRIBE, {change: 123});

	expect(ran.deny).toBe(
		'change 123 was not subscribed: MCP server "gerrit" is not ready (credentials_rejected): status=401',
	);
	expect(calls).toEqual([]);
});

test('a subscription that succeeds after a refusal clears the cause from the band', async ($, on) => {
	const {server} = world(on, [[START]], undefined, REJECTED);

	await start($);
	await run($, SUBSCRIBE, {change: 123});

	server.info = READY;
	await run($, SUBSCRIBE, {change: 123});

	expect(await band($)).toEqual({text: '· subscribed to change', hrefs: [URL_123]});
});

test('a server without get_review_activity is refused at subscribe', async ($, on) => {
	const {calls} = world(on, [[START]], undefined, {...READY, tools: ['get_change']});

	await start($);
	const ran = await run($, SUBSCRIBE, {change: 123});

	expect(ran.deny).toBe(
		'change 123 was not subscribed: MCP server "gerrit" does not offer get_review_activity: enable its read group, ' +
			'and keep the tool out of its tool filters',
	);
	expect(calls).toEqual([]);
});

const ACTIVITY_FAILED =
	'get_review_activity on "gerrit" failed: [{"type":"text","text":"unknown tool \\"get_review_activity\\""}]';

test('a failed read of a ready server is refused at subscribe, and the band says so', async ($, on) => {
	world(on, [FAILED]);

	await start($);
	const ran = await run($, SUBSCRIBE, {change: 123});

	expect(ran.deny).toBe(`change 123 was not read: ${ACTIVITY_FAILED}`);
	expect(await band($)).toEqual({text: `· ${ACTIVITY_FAILED}`, hrefs: []});
});

test('a failed poll of a ready server names its own error in the band', async ($, on) => {
	const {clock} = world(on, [[START], FAILED]);

	on('ui.log', () => ({value: undefined}));

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);

	expect(await band($)).toEqual({text: `· ${ACTIVITY_FAILED}`, hrefs: []});
});

test('a failed poll names the cause in the band until a poll succeeds', async ($, on) => {
	const {server, clock} = world(on, [[START], FAILED, [{...ACTIVITY, url: URL_123}]]);

	on('ui.log', () => ({value: undefined}));

	await start($);
	await run($, SUBSCRIBE, {change: 123});

	server.info = REJECTED;
	await clock.advance(POLL_INTERVAL_MS);

	expect(await band($)).toEqual({
		text: '· MCP server "gerrit" is not ready (credentials_rejected): status=401',
		hrefs: [],
	});

	server.info = READY;
	await clock.advance(POLL_INTERVAL_MS);

	expect(await band($)).toEqual({text: '· subscribed to change', hrefs: [URL_123]});
});

test('unsubscribe stops the polling of that change', async ($, on) => {
	const {calls, clock} = world(on, [[START]]);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await run($, UNSUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);

	expect(calls.length).toBe(1);
});

test('activity that the server answers after an unsubscribe is not delivered', async ($, on) => {
	const {prompts, clock, session} = world(on, [[START], [ACTIVITY]], async (call) =>
		call === 2 ? run($, UNSUBSCRIBE, {change: 123}) : undefined,
	);

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([]);
	expect(session.appended()).toEqual([]);
});

test('an answer to an earlier subscription leaves the starting point of a new one', async ($, on) => {
	const restart = {...START, since: '2026-07-03T09:00:00Z'};
	const {calls, prompts, clock} = world(on, [[START], [ACTIVITY], [restart], [restart]], async (call) => {
		if (call === 2) {
			await run($, UNSUBSCRIBE, {change: 123});
			await run($, SUBSCRIBE, {change: 123});
		}
	});

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await clock.advance(POLL_INTERVAL_MS);
	await clock.advance(POLL_INTERVAL_MS);

	expect(prompts).toEqual([]);
	expect(calls.at(-1)).toEqual({changes: [{change: 123, since: restart.since, status: 'NEW'}]});
});
