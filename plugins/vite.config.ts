import {defineConfig} from 'vite-plus';
import javascript from '@ver0/oxlint-config/javascript.js';
import node from '@ver0/oxlint-config/node.js';
import typescript from '@ver0/oxlint-config/typescript.js';

export default defineConfig({
	fmt: {
		printWidth: 120,
		useTabs: true,
		tabWidth: 2,
		semi: true,
		singleQuote: true,
		trailingComma: 'all',
		bracketSpacing: false,
		// constraint: rumdl formats markdown, configured in .rumdl.toml
		ignorePatterns: ['**/LICENSE', '**/*.md'],
	},
	lint: {
		// constraint: lint and type-check cover the files of this directory itself, and no mod source. A mod
		// type-checks against types that Claude Code writes only when it loads the mod interactively; no CLI command
		// writes them, and a checkout without them fails every lint run with an invalid tsconfig. vp fmt formats the mod
		// sources, and claude plugin test runs their tests
		ignorePatterns: ['*/**/*.ts', '*/**/*.tsx'],
		extends: [javascript, typescript, node],
		options: {typeAware: true, typeCheck: true},
	},
});
