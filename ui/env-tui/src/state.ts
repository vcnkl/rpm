export type RuntimeEvent = {
	type: string
	ref?: string
	stream?: string
	line?: string
	error?: string
}

export type UnitState = {
	ref: string
	kind: 'target' | 'dependency' | 'environment'
	status: 'starting' | 'running' | 'reloading' | 'exited' | 'failed' | 'stopped'
	output: string[]
}

export type EnvState = {
	blueprint: string
	selected: number
	showDependencies: boolean
	units: UnitState[]
}

const maxLines = 200
const statusOrder: Record<UnitState['status'], number> = {
	failed: 0,
	reloading: 1,
	starting: 2,
	running: 3,
	exited: 4,
	stopped: 5
}

export function initialState(blueprint = 'environment'): EnvState {
	return { blueprint, selected: 0, showDependencies: true, units: [] }
}

export function reduceEvent(state: EnvState, event: RuntimeEvent): EnvState {
	let next: EnvState
	switch (event.type) {
		case 'process_started':
			next = upsertUnit(state, event.ref, 'target', 'running')
			break
		case 'process_output':
			next = appendOutput(upsertUnit(state, event.ref, 'target', 'running'), event.ref, event.line ?? '')
			break
		case 'process_exited':
			next = upsertUnit(state, event.ref, 'target', event.error ? 'failed' : 'exited')
			break
		case 'dependency_started':
			next = upsertUnit(state, event.ref, 'dependency', 'running')
			break
		case 'dependency_failed':
			next = upsertUnit(state, event.ref ?? 'dependencies', 'dependency', 'failed')
			break
		case 'reload_started':
			next = upsertUnit(state, event.ref, 'target', 'reloading')
			break
		case 'reload_completed':
			next = upsertUnit(state, event.ref, 'target', event.error ? 'failed' : 'running')
			break
		case 'environment_stopped':
			next = {
				...state,
				units: state.units.map((unit) => ({ ...unit, status: unit.status === 'failed' ? 'failed' : 'stopped' }))
			}
			break
		default:
			return state
	}
	return clampSelection(next)
}

export function orderUnits(units: UnitState[], showDependencies = true): UnitState[] {
	return units
		.filter((unit) => showDependencies || unit.kind !== 'dependency')
		.slice()
		.sort((a, b) => {
			if (a.kind !== b.kind) return a.kind === 'dependency' ? -1 : 1
			if (statusOrder[a.status] !== statusOrder[b.status]) return statusOrder[a.status] - statusOrder[b.status]
			return a.ref.localeCompare(b.ref)
		})
}

export type UnitSummary = {
	total: number
	dependencies: number
	targets: number
	running: number
	reloading: number
	failed: number
	stopped: number
}

export function summarizeUnits(units: UnitState[]): UnitSummary {
	return units.reduce(
		(summary, unit) => {
			summary.total += 1
			if (unit.kind === 'dependency') summary.dependencies += 1
			if (unit.kind === 'target') summary.targets += 1
			if (unit.status === 'running') summary.running += 1
			if (unit.status === 'reloading') summary.reloading += 1
			if (unit.status === 'failed') summary.failed += 1
			if (unit.status === 'stopped') summary.stopped += 1
			return summary
		},
		{ total: 0, dependencies: 0, targets: 0, running: 0, reloading: 0, failed: 0, stopped: 0 } as UnitSummary
	)
}

export function clampSelection(state: EnvState): EnvState {
	const visible = orderUnits(state.units, state.showDependencies)
	const selected = Math.max(0, Math.min(visible.length - 1, state.selected))
	return selected === state.selected ? state : { ...state, selected }
}

export function visibleWindow<T>(items: T[], selected: number, height: number): { start: number; rows: T[] } {
	if (height <= 0 || items.length === 0) return { start: 0, rows: [] }
	const bounded = Math.max(0, Math.min(items.length - 1, selected))
	const start = Math.max(0, Math.min(bounded - Math.floor(height / 2), items.length - height))
	return { start, rows: items.slice(start, start + height) }
}

export type KeyAction =
	| { type: 'select'; delta: number }
	| { type: 'restart' }
	| { type: 'restart_all' }
	| { type: 'toggle_dependencies' }
	| { type: 'quit' }
	| { type: 'none' }

export function actionForKey(input: string, key: { upArrow?: boolean; downArrow?: boolean }): KeyAction {
	if (key.upArrow || input === 'k') return { type: 'select', delta: -1 }
	if (key.downArrow || input === 'j') return { type: 'select', delta: 1 }
	if (input === 'r') return { type: 'restart' }
	if (input === 'R') return { type: 'restart_all' }
	if (input === 'd') return { type: 'toggle_dependencies' }
	if (input === 'q') return { type: 'quit' }
	return { type: 'none' }
}

function upsertUnit(
	state: EnvState,
	ref = 'environment',
	kind: UnitState['kind'],
	status: UnitState['status']
): EnvState {
	const units = state.units.slice()
	const index = units.findIndex((unit) => unit.ref === ref)
	if (index >= 0) {
		units[index] = { ...units[index], kind, status }
	} else {
		units.push({ ref, kind, status, output: [] })
	}
	return { ...state, units }
}

function appendOutput(state: EnvState, ref = 'environment', line: string): EnvState {
	return {
		...state,
		units: state.units.map((unit) => {
			if (unit.ref !== ref) return unit
			return { ...unit, output: [...unit.output, line].slice(-maxLines) }
		})
	}
}
