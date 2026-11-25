export type Subscription = {since: string; status: string; url?: string};

declare module 'claude-code' {
	interface PluginState {
		'gerrit-review-notifications': {
			subscriptions: Shaped<Record<string, Subscription>>;
			isMainTurnRunning: boolean;
			serverProblem: string;
		};
	}
}
