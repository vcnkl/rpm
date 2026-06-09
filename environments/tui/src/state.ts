export type RuntimeEvent = {
	type: string
	ref?: string
	bundle?: string
	name?: string
	kind?: UnitState['kind']
	status?: UnitState['status']
	message?: string
	stream?: string
	line?: string
	error?: string
}

export type UnitState = {
	ref: string
	bundle?: string
	name?: string
	kind: 'target' | 'before' | 'dependency' | 'environment'
	status: 'pending' | 'starting' | 'running' | 'reloading' | 'exited' | 'failed' | 'stopped'
	output: string[]
	error?: string
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
	pending: 4,
	exited: 4,
	stopped: 6
}

const kindOrder: Record<UnitState['kind'], number> = {
	dependency: 0,
	before: 1,
	target: 2,
	environment: 3
}

export function initialState(blueprint = 'environment'): EnvState {
	return { blueprint, selected: 0, showDependencies: true, units: [] }
}

export function reduceEvent(state: EnvState, event: RuntimeEvent): EnvState {
	let next: EnvState
	switch (event.type) {
		case 'unit_declared':
			next = upsertUnit(state, event.ref, event.kind ?? 'target', event.status ?? 'pending', event)
			break
		case 'process_started':
			next = upsertUnit(state, event.ref, 'target', 'running')
			break
		case 'process_output':
			next = appendOutput(upsertUnit(state, event.ref, 'target', 'running'), event.ref, event.line ?? '')
			break
		case 'process_exited':
			next = upsertUnit(state, event.ref, 'target', event.error ? 'failed' : 'exited', event)
			if (event.error) next = appendOutput(next, event.ref, event.error)
			break
		case 'dependency_started':
			next = upsertUnit(state, event.ref, 'dependency', 'running')
			break
		case 'dependency_failed':
			next = upsertUnit(state, event.ref ?? 'dependencies', 'dependency', 'failed', event)
			next = appendOutput(next, event.ref ?? 'dependencies', event.error ?? event.message ?? 'dependency failed')
			break
		case 'reload_started':
			next = upsertUnit(state, event.ref, 'target', 'reloading')
			break
		case 'reload_completed':
			next = upsertUnit(state, event.ref, 'target', event.error ? 'failed' : 'running', event)
			if (event.error) next = appendOutput(next, event.ref, event.error)
			break
		case 'environment_stopped':
			next = {
				...state,
			units: state.units.map((unit) => ({ ...unit, status: unit.status === 'failed' ? 'failed' : 'stopped' }))
			}
			if (event.error || event.message) {
				next = appendOutput(next, event.ref ?? 'environment', event.error ?? event.message ?? '')
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
			if (a.kind !== b.kind) return kindOrder[a.kind] - kindOrder[b.kind]
			if (statusOrder[a.status] !== statusOrder[b.status]) return statusOrder[a.status] - statusOrder[b.status]
			return a.ref.localeCompare(b.ref)
		})
}

export type UnitSummary = {
	total: number
	dependencies: number
	targets: number
	before: number
	pending: number
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
			if (unit.kind === 'before') summary.before += 1
			if (unit.status === 'pending') summary.pending += 1
			if (unit.status === 'running') summary.running += 1
			if (unit.status === 'reloading') summary.reloading += 1
			if (unit.status === 'failed') summary.failed += 1
			if (unit.status === 'stopped') summary.stopped += 1
			return summary
		},
		{ total: 0, dependencies: 0, targets: 0, before: 0, pending: 0, running: 0, reloading: 0, failed: 0, stopped: 0 } as UnitSummary
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

export type SelectionItem = {
	ref?: string
	label: string
	detail?: string
	group?: string
	status?: string
	selected?: boolean
	defaults?: boolean
	header?: boolean
	muted?: boolean
	expanded?: boolean
	expandable?: boolean
	hidden?: boolean
}

export type SelectionRequest = {
	title: string
	items: SelectionItem[]
	requireOne?: boolean
}

export type SelectionState = {
	title: string
	items: SelectionItem[]
	cursor: number
}

export type SelectionKeyAction =
	| { type: 'select'; delta: number }
	| { type: 'toggle' }
	| { type: 'expand' }
	| { type: 'confirm' }
	| { type: 'cancel' }
	| { type: 'none' }

export function initialSelectionState(request: SelectionRequest): SelectionState {
	const cursor = request.items.findIndex((item) => !item.header)
	return { title: request.title, items: request.items, cursor: Math.max(0, cursor) }
}

export function reduceSelectionAction(state: SelectionState, action: SelectionKeyAction): SelectionState {
	if (action.type === 'select') return moveSelection(state, action.delta)
	if (action.type === 'toggle') return toggleSelection(state)
	if (action.type === 'expand') return toggleGroupExpansion(state)
	return state
}

export function selectedSelectionRefs(state: SelectionState): string[] {
	return state.items
		.filter((item) => item.selected && !item.header && !item.expandable && item.ref)
		.map((item) => item.ref as string)
		.sort()
}

export function actionForSelectionKey(
	input: string,
	key: { upArrow?: boolean; downArrow?: boolean; return?: boolean; escape?: boolean; tab?: boolean }
): SelectionKeyAction {
	if (key.upArrow || input === 'k') return { type: 'select', delta: -1 }
	if (key.downArrow || input === 'j') return { type: 'select', delta: 1 }
	if (input === ' ') return { type: 'toggle' }
	if (key.tab || input === '\t') return { type: 'expand' }
	if (key.return) return { type: 'confirm' }
	if (key.escape) return { type: 'cancel' }
	return { type: 'none' }
}

function moveSelection(state: SelectionState, delta: number): SelectionState {
	let next = state.cursor
	for (;;) {
		next += delta
		if (next < 0 || next >= state.items.length) return state
		if (state.items[next].hidden) continue
		if (!state.items[next].header || state.items[next].expandable) return { ...state, cursor: next }
	}
}

function toggleSelection(state: SelectionState): SelectionState {
	const item = state.items[state.cursor]
	if (!item) return state
	if (item.expandable) return toggleGroupSelection(state, item)
	if (item.header) return state
	const items = state.items.slice()
	items[state.cursor] = { ...item, selected: !item.selected }
	return { ...state, items }
}

function toggleGroupSelection(state: SelectionState, group: SelectionItem): SelectionState {
	const refs = visibleGroupIndexes(state.items, group)
	const shouldSelect = refs.some((index) => !state.items[index].selected)
	const items = state.items.map((item, index) => {
		if (!refs.includes(index)) return item
		return { ...item, selected: shouldSelect }
	})
	return { ...state, items }
}

function toggleGroupExpansion(state: SelectionState): SelectionState {
	const item = state.items[state.cursor]
	if (!item?.expandable) return state
	const expanded = !item.expanded
	const items = state.items.map((candidate) => {
		if (candidate.group !== item.group) return candidate
		if (candidate.expandable) return { ...candidate, expanded }
		return { ...candidate, hidden: !expanded }
	})
	return { ...state, items }
}

function visibleGroupIndexes(items: SelectionItem[], group: SelectionItem): number[] {
	return items.flatMap((item, index) => {
		if (item.group !== group.group || item.header || item.expandable || item.hidden) return []
		return [index]
	})
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
	status: UnitState['status'],
	event?: RuntimeEvent
): EnvState {
	const units = state.units.slice()
	const index = units.findIndex((unit) => unit.ref === ref)
	if (index >= 0) {
		units[index] = {
			...units[index],
			bundle: event?.bundle ?? units[index].bundle,
			name: event?.name ?? units[index].name,
			kind: units[index].kind === 'environment' ? kind : units[index].kind,
			status,
			error: event?.error ?? units[index].error
		}
	} else {
		const unit: UnitState = { ref, kind, status, output: [] }
		if (event?.bundle) unit.bundle = event.bundle
		if (event?.name) unit.name = event.name
		if (event?.error) unit.error = event.error
		units.push(unit)
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
