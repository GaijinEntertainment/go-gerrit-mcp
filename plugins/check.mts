import {spawnSync} from 'node:child_process';
import {existsSync, readdirSync, readFileSync} from 'node:fs';
import path from 'node:path';

const pluginsDir = import.meta.dirname;
const root = path.dirname(pluginsDir);
const failures: string[] = [];

function claude(...args: readonly string[]): void {
	const ran = spawnSync('claude', args, {cwd: root, encoding: 'utf8'});

	if (ran.error !== undefined) {
		throw new Error('claude did not run; install it with `npm install -g @anthropic-ai/claude-code`', {
			cause: ran.error,
		});
	}

	if (ran.status !== 0) {
		failures.push(`claude ${args.join(' ')} failed:\n${ran.stdout}${ran.stderr}`);
	}
}

function isMod(plugin: string): boolean {
	const hooks = path.join(pluginsDir, plugin, 'hooks', 'hooks.json');

	if (!existsSync(hooks)) {
		return false;
	}

	const manifest: unknown = JSON.parse(readFileSync(hooks, 'utf8'));

	return (
		typeof manifest === 'object' &&
		manifest !== null &&
		'modules' in manifest &&
		Array.isArray(manifest.modules) &&
		manifest.modules.length > 0
	);
}

const plugins = readdirSync(pluginsDir, {withFileTypes: true})
	.filter(
		(entry) => entry.isDirectory() && existsSync(path.join(pluginsDir, entry.name, '.claude-plugin', 'plugin.json')),
	)
	.map((entry) => entry.name);
const license = readFileSync(path.join(root, 'LICENSE'));

claude('plugin', 'validate', '.');

for (const plugin of plugins) {
	const dir = path.join('plugins', plugin);

	claude('plugin', 'validate', dir);

	if (isMod(plugin)) {
		claude('plugin', 'test', dir);
	}

	const pluginLicense = path.join(root, dir, 'LICENSE');

	if (!existsSync(pluginLicense) || !readFileSync(pluginLicense).equals(license)) {
		failures.push(`${dir}/LICENSE is not a copy of the root LICENSE`);
	}
}

for (const failure of failures) {
	console.error(failure);
}

console.log(`checked ${plugins.length} plugin(s): ${failures.length} failure(s)`);
process.exitCode = failures.length === 0 ? 0 : 1;
