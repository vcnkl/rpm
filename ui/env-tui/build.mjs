import esbuild from 'esbuild'

const reactDevtoolsStub = {
	name: 'react-devtools-core-stub',
	setup(build) {
		build.onResolve({ filter: /^react-devtools-core$/ }, () => ({
			path: 'react-devtools-core',
			namespace: 'react-devtools-core-stub'
		}))
		build.onLoad({ filter: /^react-devtools-core$/, namespace: 'react-devtools-core-stub' }, () => ({
			contents: 'export default { connectToDevTools() {} }',
			loader: 'js'
		}))
	}
}

await esbuild.build({
	entryPoints: ['src/app.tsx'],
	bundle: true,
	platform: 'node',
	format: 'esm',
	outfile: 'dist/index.js',
	banner: {
		js: '#!/usr/bin/env node'
	},
	plugins: [reactDevtoolsStub]
})
