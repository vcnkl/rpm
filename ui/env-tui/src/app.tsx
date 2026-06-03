import React, { useEffect, useMemo, useReducer, useState } from 'react'
import { Box, render, Text, useApp, useInput } from 'ink'
import {
	actionForSelectionKey,
	actionForKey,
	clampSelection,
	initialState,
	initialSelectionState,
	orderUnits,
	reduceEvent,
	reduceSelectionAction,
	selectedSelectionRefs,
	summarizeUnits,
	visibleWindow,
	type EnvState,
	type RuntimeEvent,
	type SelectionItem,
	type SelectionRequest,
	type SelectionState,
	type UnitState
} from './state.js'
import fs from 'node:fs'
import process from 'node:process'

type StateAction =
	| { type: 'event'; event: RuntimeEvent }
	| { type: 'select'; delta: number }
	| { type: 'toggle_dependencies' }

function reducer(state: EnvState, action: StateAction): EnvState {
	if (action.type === 'event') return reduceEvent(state, action.event)
	if (action.type === 'toggle_dependencies')
		return clampSelection({ ...state, showDependencies: !state.showDependencies, selected: 0 })
	const visible = orderUnits(state.units, state.showDependencies)
	const next = Math.max(0, Math.min(visible.length - 1, state.selected + action.delta))
	return { ...state, selected: next }
}

function App() {
	const { exit } = useApp()
	const [state, dispatch] = useReducer(reducer, initialState(process.env.RPM_ENV_BLUEPRINT ?? 'environment'))
	const viewport = useTerminalSize()
	const visible = useMemo(() => orderUnits(state.units, state.showDependencies), [state.units, state.showDependencies])
	const summary = useMemo(() => summarizeUnits(state.units), [state.units])
	const bodyHeight = Math.max(4, viewport.rows - 5)
	const listRows = Math.max(1, bodyHeight - 2)
	const windowed = visibleWindow(visible, state.selected, listRows)
	const selected = visible[state.selected]

	useEffect(() => {
		const eventStream = fs.createReadStream('/dev/fd/3', { encoding: 'utf8' })
		let buffer = ''
		let exitTimer: NodeJS.Timeout | undefined
		const hadError = { current: false }
		const parseLine = (line: string) => {
			if (!line.trim()) return
			try {
				const event = JSON.parse(line) as RuntimeEvent
				if (event.error || event.type === 'dependency_failed') hadError.current = true
				dispatch({ type: 'event', event })
			} catch {
				// Ignore malformed event lines so one bad write does not kill the TUI.
			}
		}
		const onData = (chunk: string | Buffer) => {
			buffer += chunk.toString()
			const lines = buffer.split('\n')
			buffer = lines.pop() ?? ''
			for (const line of lines) parseLine(line)
		}
		const onEnd = () => {
			parseLine(buffer)
			buffer = ''
			dispatch({ type: 'event', event: { type: 'environment_stopped' } })
			if (!hadError.current) exitTimer = setTimeout(() => exit(), 80)
		}
		const onError = (error: Error) => {
			dispatch({ type: 'event', event: { type: 'environment_stopped', message: error.message } })
			hadError.current = true
		}
		eventStream.on('data', onData)
		eventStream.on('end', onEnd)
		eventStream.on('error', onError)
		return () => {
			if (exitTimer) clearTimeout(exitTimer)
			eventStream.off('data', onData)
			eventStream.off('end', onEnd)
			eventStream.off('error', onError)
			eventStream.destroy()
		}
	}, [exit])

	useInput((input, key) => {
		const action = actionForKey(input, key)
		if (action.type === 'select') dispatch(action)
		if (action.type === 'toggle_dependencies') dispatch(action)
		if (action.type === 'restart' && selected?.kind === 'target') send({ type: 'restart', ref: selected.ref })
		if (action.type === 'restart_all') send({ type: 'restart_all' })
		if (action.type === 'quit') {
			send({ type: 'quit' })
			exit()
		}
	})

	return (
		<Box width={viewport.columns} height={viewport.rows} flexDirection="column">
			<Box width={viewport.columns} justifyContent="space-between" paddingX={1}>
				<Text bold color="cyan">
					rpm env {state.blueprint}
				</Text>
				<RuntimeSummary summary={summary} />
			</Box>
			<Box width={viewport.columns} justifyContent="space-between" paddingX={1}>
				<Box>
					<LabelValue label="targets" value={String(summary.targets)} color="cyan" />
					<Separator />
					<LabelValue label="before" value={String(summary.before)} color="yellow" />
					<Separator />
					<LabelValue label="deps" value={String(summary.dependencies)} color="magenta" />
					<Separator />
					<LabelValue label="deps view" value={state.showDependencies ? 'shown' : 'hidden'} color="gray" />
				</Box>
				<HelpBar
					hints={[
						['up/down', 'move'],
						['r', 'restart'],
						['R', 'all'],
						['d', 'deps'],
						['q', 'quit']
					]}
				/>
			</Box>
			<Box width={viewport.columns} height={bodyHeight} paddingX={1}>
				<Box
					width={Math.max(30, Math.floor(viewport.columns * 0.42))}
					height={bodyHeight}
					flexDirection="column"
					borderStyle="single"
					borderColor="cyan"
					paddingX={1}
				>
					<Box justifyContent="space-between">
						<Text bold color="cyan">
							Units
						</Text>
						<Text color="gray">
							{visible.length > listRows
								? `${windowed.start + 1}-${windowed.start + windowed.rows.length}/${visible.length}`
								: `${visible.length}`}
						</Text>
					</Box>
					{windowed.rows.map((unit, index) => {
						const absolute = windowed.start + index
						return <UnitRow key={unit.ref} unit={unit} selected={absolute === state.selected} />
					})}
				</Box>
				<Box width={1} />
				<Box
					flexGrow={1}
					height={bodyHeight}
					flexDirection="column"
					borderStyle="single"
					borderColor="blue"
					paddingX={1}
				>
					<Box justifyContent="space-between">
						<Text bold color={selected ? statusColor(selected.status) : 'gray'}>
							{selected ? selected.ref : 'No runtime units yet'}
						</Text>
						<StatusBadge status={selected?.status} />
					</Box>
					{!selected && (
						<Box marginTop={1}>
							<Text color="gray">Waiting for dependency and target events...</Text>
						</Box>
					)}
					{(selected?.output ?? []).slice(-(bodyHeight - 3)).map((line, index) => (
						<Text key={index} wrap="truncate" color={selected?.status === 'failed' ? 'red' : undefined}>
							{line}
						</Text>
					))}
				</Box>
			</Box>
			<Box width={viewport.columns} justifyContent="space-between" paddingX={1}>
				<Text color={selected ? statusColor(selected.status) : 'gray'}>
					{summary.failed > 0
						? 'startup failed - press q to exit'
						: selected
							? `${selected.kind} ${selected.status}`
							: 'waiting for runtime events'}
				</Text>
				<Text color="gray">
					press <Text color="cyan">q</Text> to stop
				</Text>
			</Box>
		</Box>
	)
}

function RuntimeSummary({ summary }: { summary: ReturnType<typeof summarizeUnits> }) {
	return (
		<Box>
			<LabelValue label="running" value={String(summary.running)} color="green" />
			<Separator />
			<LabelValue label="reloading" value={String(summary.reloading)} color="yellow" />
			<Separator />
			<LabelValue label="pending" value={String(summary.pending)} color="gray" />
			<Separator />
			<LabelValue label="failed" value={String(summary.failed)} color={summary.failed > 0 ? 'red' : 'gray'} />
		</Box>
	)
}

function LabelValue({ label, value, color }: { label: string; value: string; color: string }) {
	return (
		<Box marginRight={1}>
			<Text color="gray">{label} </Text>
			<Text bold color={color}>
				{value}
			</Text>
		</Box>
	)
}

function Separator() {
	return (
		<Box marginRight={1}>
			<Text color="gray">|</Text>
		</Box>
	)
}

function HelpBar({ hints }: { hints: Array<[string, string]> }) {
	return (
		<Box>
			{hints.map(([key, label], index) => (
				<Box key={`${key}:${label}`} marginLeft={index === 0 ? 0 : 1}>
					<Text color="cyan">{key}</Text>
					<Text color="gray"> {label}</Text>
				</Box>
			))}
		</Box>
	)
}

function StatusBadge({ status }: { status?: UnitState['status'] }) {
	return <Text color={status ? statusColor(status) : 'gray'}>{status ?? 'idle'}</Text>
}

type SelectionAction = { type: 'select'; delta: number } | { type: 'toggle' } | { type: 'expand' }

function selectionReducer(state: SelectionState, action: SelectionAction): SelectionState {
	return reduceSelectionAction(state, action)
}

function SelectionApp() {
	const [request, setRequest] = useState<SelectionRequest | null>(null)

	useEffect(() => {
		const input = fs.createReadStream('/dev/fd/3', { encoding: 'utf8' })
		let buffer = ''
		const onData = (chunk: string | Buffer) => {
			buffer += chunk.toString()
		}
		const onEnd = () => {
			setRequest(JSON.parse(buffer) as SelectionRequest)
		}
		input.on('data', onData)
		input.on('end', onEnd)
		return () => {
			input.off('data', onData)
			input.off('end', onEnd)
			input.destroy()
		}
	}, [])

	if (!request) return <Text color="gray">Loading selection...</Text>
	return <SelectionView request={request} />
}

function SelectionView({ request }: { request: SelectionRequest }) {
	const { exit } = useApp()
	const [state, dispatch] = useReducer(selectionReducer, request, initialSelectionState)
	const viewport = useTerminalSize()
	const bodyHeight = Math.max(4, viewport.rows - 5)
	const visibleItems = state.items.filter((item) => !item.hidden)
	const visibleCursor = Math.max(0, visibleItems.findIndex((item) => item === state.items[state.cursor]))
	const windowed = visibleWindow(visibleItems, visibleCursor, bodyHeight)
	const selectedCount = selectedSelectionRefs(state).length

	useEffect(() => {
		if (process.env.RPM_ENV_TUI_AUTO_CONFIRM === '1') {
			process.stdout.write(JSON.stringify({ refs: selectedSelectionRefs(state) }) + '\n')
			exit()
		}
	}, [exit, state])

	useInput((input, key) => {
		const action = actionForSelectionKey(input, key)
		if (action.type === 'select' || action.type === 'toggle') dispatch(action)
		if (action.type === 'confirm') {
			const refs = selectedSelectionRefs(state)
			if (!request.requireOne || refs.length > 0) {
				process.stdout.write(JSON.stringify({ refs }) + '\n')
				exit()
			}
		}
		if (action.type === 'cancel') {
			process.exitCode = 130
			exit()
		}
	})

	return (
		<Box width={viewport.columns} height={viewport.rows} flexDirection="column">
			<Box width={viewport.columns} justifyContent="space-between" paddingX={1}>
				<Text bold color="cyan">
					{state.title}
				</Text>
				<LabelValue
					label="selected"
					value={String(selectedCount)}
					color={request.requireOne && selectedCount === 0 ? 'red' : 'green'}
				/>
			</Box>
			<Box width={viewport.columns} justifyContent="space-between" paddingX={1}>
				<HelpBar
					hints={[
					['space', 'toggle'],
					['tab', 'expand'],
					['enter', 'accept'],
					['esc', 'cancel']
					]}
				/>
				<Text color="gray">
					{state.items.length > bodyHeight
						? `${windowed.start + 1}-${windowed.start + windowed.rows.length}/${visibleItems.length}`
						: `${visibleItems.length}`}
				</Text>
			</Box>
			<Box
				width={viewport.columns}
				height={bodyHeight}
				flexDirection="column"
				paddingX={1}
				borderStyle="single"
				borderColor="cyan"
			>
				{windowed.rows.map((item) => {
					const absolute = state.items.indexOf(item)
					return (
						<SelectionRow
							key={`${absolute}:${item.ref ?? item.label}`}
							item={item}
							selected={absolute === state.cursor}
						/>
					)
				})}
			</Box>
			<Box width={viewport.columns} justifyContent="space-between" paddingX={1}>
				<HelpBar
					hints={[
						['up/down', 'move'],
						['j/k', 'move']
					]}
				/>
				<Text color={request.requireOne && selectedCount === 0 ? 'red' : 'gray'}>
					{request.requireOne && selectedCount === 0 ? 'select at least one item' : 'ready'}
				</Text>
			</Box>
		</Box>
	)
}

function SelectionRow({ item, selected }: { item: SelectionItem; selected: boolean }) {
	if (item.header) {
		return (
			<Text bold color={item.muted ? 'gray' : 'yellow'} wrap="truncate">
				{item.label.toUpperCase()}
			</Text>
		)
	}
	if (item.expandable) {
		const icon = item.expanded ? 'v' : '>'
		const checked = item.selected ? '[selected]' : '[ ]'
		return (
			<Text inverse={selected} bold color={selected ? undefined : 'cyan'} wrap="truncate">
				{selected ? '>' : ' '} {icon} {item.label} {checked}
			</Text>
		)
	}
	const checked = item.selected ? '[x]' : '[ ]'
	const selectedStatus = item.status === 'disabled' ? 'Run before' : (item.status ?? 'selected')
	const status = item.selected ? `  [${selectedStatus}]` : item.status ? `  [${item.status}]` : ''
	const color = selected ? undefined : item.selected || item.defaults ? 'green' : item.muted ? 'gray' : undefined
	return (
		<Text inverse={selected} color={color} wrap="truncate">
			{selected ? '>' : ' '} {checked} {item.label}
			{status}
			{item.detail ? `  ${item.detail}` : ''}
		</Text>
	)
}

function UnitRow({ unit, selected }: { unit: UnitState; selected: boolean }) {
	const kind = unit.kind === 'dependency' ? 'dep' : unit.kind === 'before' ? 'before' : 'target'
	const error = unit.error ? `  ${unit.error}` : ''
	return (
		<Text inverse={selected} color={selected ? undefined : statusColor(unit.status)} wrap="truncate">
			{selected ? '>' : ' '} {statusSymbol(unit.status)} {kind.padEnd(6)} {unit.ref}
			{error}
		</Text>
	)
}

function statusColor(status: UnitState['status']) {
	switch (status) {
		case 'failed':
			return 'red'
		case 'reloading':
		case 'starting':
			return 'cyan'
		case 'running':
			return 'green'
		case 'pending':
			return 'gray'
		case 'exited':
		case 'stopped':
			return 'gray'
	}
}

function statusSymbol(status: UnitState['status']) {
	switch (status) {
		case 'failed':
			return '!'
		case 'reloading':
			return '~'
		case 'starting':
			return '+'
		case 'running':
			return '*'
		case 'pending':
			return '.'
		case 'exited':
			return 'o'
		case 'stopped':
			return '-'
	}
}

function useTerminalSize() {
	const read = () => ({
		columns: Math.max(60, process.stderr.columns ?? 100),
		rows: Math.max(12, process.stderr.rows ?? 30)
	})
	const [size, setSize] = useState(read)

	useEffect(() => {
		const onResize = () => setSize(read())
		process.stderr.on('resize', onResize)
		return () => {
			process.stderr.off('resize', onResize)
		}
	}, [])

	return size
}

function send(action: { type: string; ref?: string }) {
	process.stdout.write(JSON.stringify(action) + '\n')
}

render(process.env.RPM_ENV_TUI_MODE === 'select' ? <SelectionApp /> : <App />, { stdout: process.stderr })
