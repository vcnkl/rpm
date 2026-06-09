import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'rpm-env-tui-smoke-'))
const bundlePath = path.join(tmpDir, 'index.js')
const payloadPath = path.join(tmpDir, 'selection.json')
fs.copyFileSync(new URL('dist/index.js', import.meta.url), bundlePath)
fs.writeFileSync(
	payloadPath,
	JSON.stringify({
		title: 'Smoke selection',
		requireOne: true,
		items: [{ ref: 'api:dev', label: 'api:dev', selected: true }]
	})
)
const payloadFd = fs.openSync(payloadPath, 'r')

const child = spawn(process.execPath, [bundlePath], {
	cwd: import.meta.dirname,
	env: { ...process.env, RPM_ENV_TUI_AUTO_CONFIRM: '1', RPM_ENV_TUI_MODE: 'select' },
	stdio: ['pipe', 'pipe', 'pipe', payloadFd]
})
fs.closeSync(payloadFd)

let stdout = ''
let stderr = ''

child.stdout.setEncoding('utf8')
child.stderr.setEncoding('utf8')
child.stdout.on('data', (chunk) => {
	stdout += chunk
})
child.stderr.on('data', (chunk) => {
	stderr += chunk
})
child.stdin.on('error', ignorePipeClose)
child.stdin.end('\r')

const result = await new Promise((resolve, reject) => {
	const timeout = setTimeout(() => {
		child.kill('SIGTERM')
		reject(new Error(`env TUI smoke test timed out\nstderr:\n${stderr}`))
	}, 5000)

	child.on('error', (error) => {
		clearTimeout(timeout)
		reject(error)
	})
	child.on('exit', (code, signal) => {
		clearTimeout(timeout)
		resolve({ code, signal })
	})
})

assert.deepEqual(result, { code: 0, signal: null }, stderr)
assert.doesNotMatch(stderr, /Dynamic require|ERR_MODULE_NOT_FOUND/)
assert.deepEqual(JSON.parse(stdout.trim()), { refs: ['api:dev'] })
fs.rmSync(tmpDir, { recursive: true, force: true })

function ignorePipeClose(error) {
	if (error.code !== 'EPIPE' && error.code !== 'ECONNRESET') {
		throw error
	}
}
