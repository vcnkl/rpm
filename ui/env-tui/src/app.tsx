import React, { useEffect, useMemo, useReducer, useState } from 'react'
import { Box, render, Text, useInput } from 'ink'
import {
	actionForKey,
	clampSelection,
	initialState,
	orderUnits,
	reduceEvent,
	summarizeUnits,
	visibleWindow,
	type EnvState,
	type RuntimeEvent,
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
	const [state, dispatch] = useReducer(reducer, initialState(process.env.RPM_ENV_BLUEPRINT ?? 'environment'))
	const viewport = useTerminalSize()
	const visible = useMemo(() => orderUnits(state.units, state.showDependencies), [state.units, state.showDependencies])
	const summary = useMemo(() => summarizeUnits(state.units), [state.units])
	const bodyHeight = Math.max(4, viewport.rows - 4)
	const listRows = Math.max(1, bodyHeight - 2)
	const windowed = visibleWindow(visible, state.selected, listRows)
	const selected = visible[state.selected]

	useEffect(() => {
		const eventStream = fs.createReadStream('/dev/fd/3', { encoding: 'utf8' })
		let buffer = ''
		const onData = (chunk: string | Buffer) => {
			buffer += chunk.toString()
			const lines = buffer.split('\n')
			buffer = lines.pop() ?? ''
			for (const line of lines) {
				if (!line.trim()) continue
				try {
					dispatch({ type: 'event', event: JSON.parse(line) as RuntimeEvent })
				} catch {
					// Ignore malformed event lines so one bad write does not kill the TUI.
				}
			}
		}
		eventStream.on('data', onData)
		return () => {
			eventStream.off('data', onData)
			eventStream.destroy()
		}
	}, [])

	useInput((input, key) => {
		const action = actionForKey(input, key)
		if (action.type === 'select') dispatch(action)
		if (action.type === 'toggle_dependencies') dispatch(action)
		if (action.type === 'restart' && selected?.kind === 'target') send({ type: 'restart', ref: selected.ref })
		if (action.type === 'restart_all') send({ type: 'restart_all' })
		if (action.type === 'quit') send({ type: 'quit' })
	})

	return (
		<Box width={viewport.columns} height={viewport.rows} flexDirection="column">
			<Box width={viewport.columns} justifyContent="space-between">
				<Text bold color="cyan">
					rpm env {state.blueprint}
				</Text>
				<Text color={summary.failed > 0 ? 'red' : summary.reloading > 0 ? 'yellow' : 'green'}>
					{summary.running} running {summary.reloading} reloading {summary.failed} failed
				</Text>
			</Box>
			<Box width={viewport.columns} justifyContent="space-between">
				<Text color="gray">
					targets {summary.targets} deps {summary.dependencies} {state.showDependencies ? 'shown' : 'hidden'}
				</Text>
				<Text color="gray">up/down move r restart R restart all d deps q quit</Text>
			</Box>
			<Box width={viewport.columns} height={bodyHeight}>
				<Box
					width={Math.max(30, Math.floor(viewport.columns * 0.42))}
					height={bodyHeight}
					flexDirection="column"
					borderStyle="single"
					paddingX={1}
				>
					<Text bold color="cyan">
						Units{' '}
						{visible.length > listRows
							? `${windowed.start + 1}-${windowed.start + windowed.rows.length}/${visible.length}`
							: `${visible.length}`}
					</Text>
					{windowed.rows.map((unit, index) => {
						const absolute = windowed.start + index
						return <UnitRow key={unit.ref} unit={unit} selected={absolute === state.selected} />
					})}
				</Box>
				<Box flexGrow={1} height={bodyHeight} flexDirection="column" borderStyle="single" paddingX={1}>
					<Box justifyContent="space-between">
						<Text bold color={selected ? statusColor(selected.status) : 'gray'}>
							{selected ? selected.ref : 'No runtime units yet'}
						</Text>
						<Text color={selected ? statusColor(selected.status) : 'gray'}>{selected?.status ?? 'idle'}</Text>
					</Box>
					{(selected?.output ?? []).slice(-(bodyHeight - 3)).map((line, index) => (
						<Text key={index} wrap="truncate">
							{line}
						</Text>
					))}
				</Box>
			</Box>
			<Box width={viewport.columns} justifyContent="space-between">
				<Text color={selected ? statusColor(selected.status) : 'gray'}>
					{selected ? `${selected.kind} ${selected.status}` : 'waiting for runtime events'}
				</Text>
				<Text color="gray">press q to stop</Text>
			</Box>
		</Box>
	)
}

function UnitRow({ unit, selected }: { unit: UnitState; selected: boolean }) {
	const kind = unit.kind === 'dependency' ? 'dep' : 'target'
	return (
		<Text inverse={selected} color={selected ? undefined : statusColor(unit.status)} wrap="truncate">
			{selected ? '>' : ' '} {statusSymbol(unit.status)} {kind.padEnd(6)} {unit.ref}
		</Text>
	)
}

function statusColor(status: UnitState['status']) {
	switch (status) {
		case 'failed':
			return 'red'
		case 'reloading':
		case 'starting':
			return 'yellow'
		case 'running':
			return 'green'
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

render(<App />, { stdout: process.stderr })
