import assert from 'node:assert/strict'
import test from 'node:test'
import {
	actionForKey,
	actionForSelectionKey,
	clampSelection,
	initialState,
	initialSelectionState,
	orderUnits,
	reduceEvent,
	reduceSelectionAction,
	selectedSelectionRefs,
	summarizeUnits,
	visibleWindow
} from './state.ts'

test('reduces runtime events into bounded unit state', () => {
	let state = initialState('local')
	state = reduceEvent(state, { type: 'process_started', ref: 'api:serve' })
	state = reduceEvent(state, { type: 'process_output', ref: 'api:serve', line: 'hello' })
	state = reduceEvent(state, { type: 'process_exited', ref: 'api:serve' })

	assert.equal(state.units[0].status, 'exited')
	assert.deepEqual(state.units[0].output, ['hello'])
})

test('orders dependencies before targets and hides dependencies when toggled', () => {
	const units = [
		{ ref: 'web:serve', kind: 'target' as const, status: 'running' as const, output: [] },
		{ ref: 'api:postgres', kind: 'dependency' as const, status: 'running' as const, output: [] },
		{ ref: 'api:serve', kind: 'target' as const, status: 'failed' as const, output: [] }
	]

	assert.deepEqual(
		orderUnits(units).map((unit) => unit.ref),
		['api:postgres', 'api:serve', 'web:serve']
	)
	assert.deepEqual(
		orderUnits(units, false).map((unit) => unit.ref),
		['api:serve', 'web:serve']
	)
})

test('summarizes runtime units for the header', () => {
	const units = [
		{ ref: 'web:serve', kind: 'target' as const, status: 'running' as const, output: [] },
		{ ref: 'api:postgres', kind: 'dependency' as const, status: 'failed' as const, output: [] },
		{ ref: 'api:serve', kind: 'target' as const, status: 'reloading' as const, output: [] }
	]

	assert.deepEqual(summarizeUnits(units), {
		total: 3,
		dependencies: 1,
		targets: 2,
		running: 1,
		reloading: 1,
		failed: 1,
		stopped: 0
	})
})

test('clamps selection and windows long lists', () => {
	const state = {
		blueprint: 'local',
		selected: 5,
		showDependencies: true,
		units: [{ ref: 'api:serve', kind: 'target' as const, status: 'running' as const, output: [] }]
	}

	assert.equal(clampSelection(state).selected, 0)
	assert.deepEqual(visibleWindow(['a', 'b', 'c', 'd', 'e'], 3, 3), { start: 2, rows: ['c', 'd', 'e'] })
})

test('maps keyboard input to TUI actions', () => {
	assert.deepEqual(actionForKey('j', {}), { type: 'select', delta: 1 })
	assert.deepEqual(actionForKey('', { upArrow: true }), { type: 'select', delta: -1 })
	assert.deepEqual(actionForKey('r', {}), { type: 'restart' })
	assert.deepEqual(actionForKey('R', {}), { type: 'restart_all' })
	assert.deepEqual(actionForKey('d', {}), { type: 'toggle_dependencies' })
	assert.deepEqual(actionForKey('q', {}), { type: 'quit' })
})

test('selection model skips headers and toggles refs', () => {
	let state = initialSelectionState({
		title: 'Select targets',
		items: [
			{ label: 'group', header: true },
			{ ref: 'api:build', label: 'api:build' },
			{ label: 'tier', header: true },
			{ ref: 'api:web_dev', label: 'api:web_dev', selected: true }
		]
	})

	assert.equal(state.cursor, 1)
	state = reduceSelectionAction(state, { type: 'select', delta: 1 })
	assert.equal(state.cursor, 3)
	state = reduceSelectionAction(state, { type: 'toggle' })

	assert.deepEqual(selectedSelectionRefs(state), [])
})

test('maps keyboard input to selection actions', () => {
	assert.deepEqual(actionForSelectionKey('j', {}), { type: 'select', delta: 1 })
	assert.deepEqual(actionForSelectionKey('', { upArrow: true }), { type: 'select', delta: -1 })
	assert.deepEqual(actionForSelectionKey(' ', {}), { type: 'toggle' })
	assert.deepEqual(actionForSelectionKey('', { return: true }), { type: 'confirm' })
	assert.deepEqual(actionForSelectionKey('', { escape: true }), { type: 'cancel' })
})
