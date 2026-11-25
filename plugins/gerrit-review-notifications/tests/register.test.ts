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

type ActivityCall = {changes: {change: number; since?: string; status?: string}[]};

// Stands for the engine and the Gerrit MCP server beneath the plugin: answers
// each get_review_activity call with the next of `answers` and records the
// calls and the submitted prompts.
const world = (on: On, answers: object[][], whileServerAnswers?: (call: number) => Promise<unknown>) => {
	const calls: ActivityCall[] = [];
	const servers: string[] = [];
	const prompts: string[] = [];

	on('session.start', (_, e) => ({cwd: e.cwd}));
	on('tool.register', (_, e) => ({value: {tool: `mcp__gerrit-review-notifications__${e.name}`}}));
	on('mcp.connect', (_, e) => ({value: {isConnected: true, server: e.server}}));
	on('mcp.call', async (_, e) => {
		calls.push(e.args as ActivityCall);
		servers.push(e.server);

		const text = JSON.stringify({changes: answers.shift()});

		await whileServerAnswers?.(calls.length);

		return {value: {content: [{type: 'text', text}], isError: false}};
	});
	on('prompt.submit', (_, e) => {
		prompts.push(e.text);

		return {text: e.text};
	});
	on('turn.start', (_, e) => ({turnId: e.turnId}));
	on('turn.complete', () => ({text: ''}));

	return {calls, servers, prompts, clock: mock.clock(on), session: mock.session(on)};
};

const run = ($: Engine, tool: typeof SUBSCRIBE | typeof UNSUBSCRIBE, input: {change: number}) =>
	$.tool.run({tool, tool_use_id: 'call', ...input});

const start = ($: Engine) => $.session.start({cwd: '/', surface: 'terminal', isInteractive: true});

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

	expect(servers).toEqual(['gerrit-work']);
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
	world(on, [[START], [{...START, change: 456}]]);

	const band = {
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
	const words = async () => {
		const ui = await $.ui.mount(band);
		const found = await ui.find({type: 'Text', text: /subscribed/});
		const link = await ui.find({type: 'Link', text: /123/});

		await ui.unmount();

		return {text: found?.text.trim(), href: link?.props.href};
	};

	await start($);
	await run($, SUBSCRIBE, {change: 123});
	await run($, SUBSCRIBE, {change: 456});

	expect(await words()).toEqual({text: '· subscribed to 2 changes:', href: URL_123});

	await run($, UNSUBSCRIBE, {change: 456});

	expect(await words()).toEqual({text: '· subscribed to change', href: URL_123});
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
