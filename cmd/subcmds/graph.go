package subcmds

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vcnkl/rpm/config"
	"github.com/vcnkl/rpm/dag"
	"github.com/vcnkl/rpm/logger"

	"github.com/urfave/cli/v2"
)

func GraphCmd() *cli.Command {
	return &cli.Command{
		Name:      "graph",
		Usage:     "Print dependency graph for debugging",
		ArgsUsage: "[target]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "format",
				Value: "text",
				Usage: "Output format: text (default), json, dot",
			},
			&cli.BoolFlag{
				Name:  "reverse",
				Usage: "Show reverse dependencies (what depends on target)",
			},
		},
		Action: func(ctx *cli.Context) error {
			debug := ctx.Bool("debug")
			format := ctx.String("format")
			reverse := ctx.Bool("reverse")

			level := logger.InfoLevel
			if debug {
				level = logger.DebugLevel
			}
			log := logger.New(level)
			_ = log

			cfg := config.NewConfig()

			graph := dag.NewGraph()
			for _, bundle := range cfg.Bundles() {
				for _, target := range bundle.Targets {
					graph.AddTarget(target)
				}
			}

			if err := graph.Resolve(cfg.Bundles()); err != nil {
				return cli.Exit("error: "+err.Error(), 1)
			}

			var targetID string
			if ctx.Args().Len() > 0 {
				targetID = ctx.Args().First()
			}

			switch format {
			case "json":
				return printJSON(graph, targetID, reverse)
			case "dot":
				return printDot(graph, targetID, reverse)
			default:
				return printText(cfg, graph, targetID, reverse)
			}
		},
	}
}

func printText(cfg *config.Config, graph *dag.Graph, targetID string, reverse bool) error {
	bundleNames := make(map[string]bool)
	for name := range cfg.Bundles() {
		bundleNames[name] = true
	}

	fmt.Println("Bundles:")
	for name := range bundleNames {
		fmt.Printf("  - %s\n", name)
	}
	fmt.Println()

	if targetID != "" {
		node, ok := graph.Nodes[targetID]
		if !ok {
			return cli.Exit("error: target not found: "+targetID, 1)
		}

		fmt.Printf("Target: %s\n", node.ID)
		if reverse {
			fmt.Println("Dependents:")
			printNodeTree(graph.Descendants(node.ID), "  ")
		} else {
			fmt.Println("Dependencies:")
			printNodeTree(node.Deps, "  ")
		}
		return nil
	}

	fmt.Println("Targets:")
	for _, node := range graph.Nodes {
		deps := make([]string, len(node.Deps))
		for i, d := range node.Deps {
			deps[i] = d.ID
		}
		if len(deps) > 0 {
			fmt.Printf("  %s -> [%s]\n", node.ID, strings.Join(deps, ", "))
		} else {
			fmt.Printf("  %s\n", node.ID)
		}
	}

	return nil
}

func printNodeTree(nodes []*dag.Node, indent string) {
	for _, n := range nodes {
		fmt.Printf("%s- %s\n", indent, n.ID)
	}
}

type GraphJSON struct {
	Nodes []NodeJSON `json:"nodes"`
	Edges []EdgeJSON `json:"edges"`
}

type NodeJSON struct {
	ID     string `json:"id"`
	Bundle string `json:"bundle"`
	Name   string `json:"name"`
}

type EdgeJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func printJSON(graph *dag.Graph, targetID string, reverse bool) error {
	var nodesToInclude map[string]*dag.Node

	if targetID != "" {
		subgraph := graph.SubgraphFor([]string{targetID})
		nodesToInclude = subgraph.Nodes
	} else {
		nodesToInclude = graph.Nodes
	}

	output := GraphJSON{
		Nodes: make([]NodeJSON, 0, len(nodesToInclude)),
		Edges: make([]EdgeJSON, 0),
	}

	for _, node := range nodesToInclude {
		parts := strings.Split(node.ID, ":")
		output.Nodes = append(output.Nodes, NodeJSON{
			ID:     node.ID,
			Bundle: parts[0],
			Name:   parts[1],
		})

		for _, dep := range node.Deps {
			if _, ok := nodesToInclude[dep.ID]; ok {
				output.Edges = append(output.Edges, EdgeJSON{
					From: node.ID,
					To:   dep.ID,
				})
			}
		}
	}

	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(data))
	return nil
}

func printDot(graph *dag.Graph, targetID string, reverse bool) error {
	fmt.Println("digraph rpm {")
	fmt.Println("  rankdir=LR;")

	for _, node := range graph.Nodes {
		fmt.Printf("  \"%s\";\n", node.ID)
		for _, dep := range node.Deps {
			if reverse {
				fmt.Printf("  \"%s\" -> \"%s\";\n", dep.ID, node.ID)
			} else {
				fmt.Printf("  \"%s\" -> \"%s\";\n", node.ID, dep.ID)
			}
		}
	}

	fmt.Println("}")
	return nil
}
