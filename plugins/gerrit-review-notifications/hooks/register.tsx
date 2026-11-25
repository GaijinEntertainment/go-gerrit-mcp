import {atom, read, update} from 'claude-code';
import type {EngineInterface, Register, Timer} from 'claude-code';

import type {Subscription} from '../types';

const PLUGIN = 'gerrit-review-notifications';
const POLL_INTERVAL_MS = 60_000;

const subscriptions = atom(
	{plugin: 'gerrit-review-notifications', key: 'subscriptions'} as const,
	{},
	{
		shape: 'subscriptions-1',
	},
);
const isMainTurnRunning = atom({plugin: 'gerrit-review-notifications', key: 'isMainTurnRunning'} as const, false);

type Subscriptions = Record<string, Subscription>;

type ActivityRequest = {change: number; since?: string; status?: string};

type ActivityEntry = ActivityRequest & {
	url?: string;
	summary?: string;
	ended?: boolean;
	content?: string;
	error?: string;
};

const SUBSCRIBE_DESCRIPTION = [
	'Subscribe this session to a Gerrit change. Call it right after you push a change for review, or when a',
	"review outcome that you depend on is pending: an approval, a CI verdict, a reviewer's reply. New activity",
	'then arrives in the session by itself as review_activity blocks that carry the change messages, votes,',
	'inline comment threads, and status transitions, so never poll the read tools for a subscribed change.',
	'When the change is merged or abandoned, a last notification says so and the subscription ends; a change',
	'that is already in such a state is refused. The subscription lives in this session only and leaves no',
	'trace on Gerrit.',
].join(' ');

const withoutChange = (subs: Subscriptions, change: string): Subscriptions => {
	const {[change]: _removed, ...rest} = subs;

	return rest;
};

const isSameSubscription = (subs: Subscriptions, request: ActivityRequest): boolean =>
	subs[String(request.change)]?.since === request.since;

const advanced = (subs: Subscriptions, entry: ActivityEntry): Subscriptions => {
	const change = String(entry.change);

	if (entry.ended === true) {
		return withoutChange(subs, change);
	}

	if (entry.since === undefined || entry.status === undefined) {
		return subs;
	}

	return {...subs, [change]: {since: entry.since, status: entry.status, url: entry.url}};
};

const ANNOUNCEMENT = 'Gerrit review activity, in full in the note from this plugin:';

// What the model reads about one entry: the server's block, unchanged.
const noted = (entry: ActivityEntry): string[] => [
	...(entry.content === undefined ? [] : [entry.content]),
	...(entry.ended === true
		? [`The subscription to change ${entry.change} ended; no more review activity on it will arrive.`]
		: []),
];

// The line of the short prompt about one entry. It holds the server's summary,
// which carries no text that a reviewer wrote.
const announced = (entry: ActivityEntry): string => {
	const words = [
		...(entry.summary === undefined ? [] : [entry.summary]),
		...(entry.ended === true ? ['subscription ended'] : []),
	];

	return words.length === 0 ? String(entry.change) : `${entry.change} · ${words.join(', ')}`;
};

let isPolling = false;
let timer: Timer | undefined;

const fetchActivity = async (
	$: EngineInterface,
	server: string,
	changes: ActivityRequest[],
): Promise<ActivityEntry[]> => {
	const connected = await $.mcp.connect(server);

	if (!connected.isConnected) {
		throw new Error(`MCP server "${server}" is not connected: ${connected.message}`);
	}

	const called = await $.mcp.call(connected.server, 'get_review_activity', {changes});

	const text = called.content[0]?.text;

	if (called.isError || text === undefined) {
		throw new Error(`get_review_activity on "${server}" failed: ${JSON.stringify(called.content)}`);
	}

	return (JSON.parse(text) as {changes: ActivityEntry[]}).changes;
};

// The model reads the whole content from the appended row. An idle session gets
// a short prompt as well: that starts the turn, and its row is what the person
// sees.
const deliver = async ($: EngineInterface, entries: ActivityEntry[]) => {
	const appended = await $.session.append({
		message: {type: 'user', content: [{type: 'text', text: entries.flatMap(noted).join('\n')}]},
	});

	if (appended.deny !== undefined) {
		throw new Error(`the session refused the review activity row: ${appended.deny}`);
	}

	if (await read($, isMainTurnRunning)) {
		return;
	}

	const submitted = await $.prompt.submit({text: [ANNOUNCEMENT, ...entries.map(announced)].join('\n')});

	if (submitted.drop !== undefined) {
		throw new Error(`the session refused the review activity prompt: ${submitted.drop}`);
	}
};

const poll = async ($: EngineInterface, server: string) => {
	if (isPolling) {
		return;
	}

	isPolling = true;

	try {
		const requests = Object.entries(await read($, subscriptions)).map(([change, {since, status}]) => ({
			change: Number(change),
			since,
			status,
		}));

		if (requests.length === 0) {
			timer?.cancel();
			timer = undefined;

			return;
		}

		const entries = await fetchActivity($, server, requests);

		for (const entry of entries) {
			if (entry.error !== undefined) {
				$.ui.log(`${PLUGIN}: change ${entry.change} was not read: ${entry.error}`);
			}
		}

		// constraint: a subscription can end, or end and start again, while the server answers; an answer counts
		// only for the subscription that asked for it
		const subsNow = await read($, subscriptions);
		const answers = requests.flatMap((request) => {
			const entry = entries.find((candidate) => candidate.change === request.change);

			return entry === undefined || entry.error !== undefined || !isSameSubscription(subsNow, request)
				? []
				: [{request, entry}];
		});
		const news = answers.map(({entry}) => entry).filter((entry) => noted(entry).length > 0);

		if (news.length > 0) {
			await deliver($, news);
		}

		await update($, subscriptions, (subs) =>
			answers.reduce(
				(kept, {request, entry}) => (isSameSubscription(kept, request) ? advanced(kept, entry) : kept),
				subs,
			),
		);
	} finally {
		isPolling = false;
	}
};

const ensurePolling = ($: EngineInterface, server: string) => {
	timer ??= $.clock.every(POLL_INTERVAL_MS, () => {
		poll($, server).catch((error: unknown) => $.ui.log(`${PLUGIN}: poll failed: ${String(error)}`));
	});
};

const ROW_VERBS: Record<string, string> = {
	'mcp__gerrit-review-notifications__subscribe_change': 'subscribe to',
	'mcp__gerrit-review-notifications__unsubscribe_change': 'unsubscribe from',
};

export const register: Register = (on, options) => {
	const server = String(options.server);

	on('session.start', async ($, e, next) => {
		await $.tool.register({
			name: 'subscribe_change',
			description: SUBSCRIBE_DESCRIPTION,
			isDeferred: false,
			inputSchema: {
				type: 'object',
				required: ['change'],
				properties: {
					change: {
						type: 'integer',
						description: 'Change number. For a Change-Id or project~number, get the number from get_change.',
					},
				},
			},
		});
		await $.tool.register({
			name: 'unsubscribe_change',
			description: 'End the subscription of this session to a Gerrit change before the change is merged or abandoned.',
			isDeferred: false,
			inputSchema: {
				type: 'object',
				required: ['change'],
				properties: {change: {type: 'integer', description: 'Change number'}},
			},
		});

		if (Object.keys(await read($, subscriptions)).length > 0) {
			ensurePolling($, server);
		}

		return next(e);
	});

	on('ui.render', {component: 'ToolUse'}, async ($, e, next) => {
		const verb = ROW_VERBS[e.props.tool];

		if (verb === undefined || e.props.isExpanded === true) {
			return next(e);
		}

		const {Box, Link, Text} = $.ui.resolve(e);
		const dot = e.props.isErrored ? 'error' : e.props.isRunning ? undefined : 'success';
		const {change} = e.props.input as {change?: number};
		const url = (await read($, subscriptions))[String(change)]?.url;

		return (
			<Box>
				<Text color={dot} dimColor={e.props.isRunning}>
					●{' '}
				</Text>
				<Text weight="semibold">Gerrit</Text>
				<Text> · {verb} change </Text>
				{url === undefined ? <Text>{change}</Text> : <Link href={url}>{change}</Link>}
			</Box>
		);
	});

	on('ui.render', {component: 'AbovePrompt'}, async ($, e, next) => {
		const changes = Object.entries(await read($, subscriptions));

		if (e.props.hasSurvey || changes.length === 0) {
			return next(e);
		}

		const {Box, Link, Text} = $.ui.resolve(e);

		return (
			<Box>
				<Text color="success">● </Text>
				<Text weight="semibold">Gerrit</Text>
				<Text dimColor> · subscribed to {changes.length === 1 ? 'change' : `${changes.length} changes:`}</Text>
				{changes.map(([change, {url}], i) => (
					<Box key={change}>
						<Text dimColor>{i === 0 ? ' ' : ', '}</Text>
						{url === undefined ? <Text dimColor>{change}</Text> : <Link href={url}>{change}</Link>}
					</Box>
				))}
			</Box>
		);
	});

	on('turn.start', async ($, e, next) => {
		if (e.agentId === undefined) {
			await update($, isMainTurnRunning, () => true);
		}

		return next(e);
	});

	on('turn.complete', async ($, e, next) => {
		if (e.agentId === undefined) {
			await update($, isMainTurnRunning, () => false);
		}

		return next(e);
	});

	on('tool.run', {tool: 'mcp__gerrit-review-notifications__subscribe_change'}, async ($, e) => {
		const input = e as unknown as {change: number};
		const change = String(input.change);

		if (!Number.isInteger(input.change)) {
			return {deny: `change must be a change number, got ${JSON.stringify(input.change)}`};
		}

		if (change in (await read($, subscriptions))) {
			return {deny: `change ${change} is already subscribed in this session`};
		}

		const [start] = await fetchActivity($, server, [{change: input.change}]);

		if (start === undefined || start.error !== undefined) {
			return {deny: `change ${change} was not read: ${start?.error ?? 'the server returned no entry'}`};
		}

		const {since, status} = start;

		if (since === undefined || status === undefined) {
			return {deny: `change ${change} was not read: ${start.summary ?? 'the server returned no point'}`};
		}

		if (start.ended === true) {
			return {deny: `change ${change} is ${status}; nothing is left to notify about`};
		}

		await update($, subscriptions, (subs) => ({...subs, [change]: {since, status, url: start.url}}));
		ensurePolling($, server);

		return {
			result:
				`Subscribed to change ${change} (status ${status}). ` +
				'New review activity on this change now arrives in this session by itself.',
		};
	});

	on('tool.run', {tool: 'mcp__gerrit-review-notifications__unsubscribe_change'}, async ($, e) => {
		const change = String((e as unknown as {change: number}).change);

		if (!(change in (await read($, subscriptions)))) {
			return {result: `Change ${change} was not subscribed.`};
		}

		await update($, subscriptions, (subs) => withoutChange(subs, change));

		return {result: `Unsubscribed from change ${change}.`};
	});
};
