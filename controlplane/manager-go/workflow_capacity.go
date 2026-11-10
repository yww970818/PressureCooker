package main

import (
	"fmt"
	"strconv"
)

type workflowCapacityLimits struct {
	Nodes, Edges int
	BodyBytes    int64
}

func workflowByteLimit() int {
	c, e := workflowCapacity()
	if e != nil {
		return 0
	}
	return int(c.BodyBytes)
}

// Keep capacity explicit and bounded. Workflow authoring can carry large JMX
// trees; unrelated API bodies retain their existing, smaller boundary.
func workflowCapacity() (workflowCapacityLimits, error) {
	integer := func(name string, def, max int) (int, error) {
		n, e := strconv.Atoi(env(name, strconv.Itoa(def)))
		if e != nil || n < 1 || n > max {
			return 0, fmt.Errorf("invalid %s", name)
		}
		return n, nil
	}
	var c workflowCapacityLimits
	var e error
	var mib int
	if c.Nodes, e = integer("JMETER_WORKFLOW_MAX_NODES", 5000, 100000); e != nil {
		return c, e
	}
	if c.Edges, e = integer("JMETER_WORKFLOW_MAX_EDGES", 10000, 200000); e != nil {
		return c, e
	}
	if mib, e = integer("JMETER_WORKFLOW_BODY_MIB", 32, 64); e != nil {
		return c, e
	}
	c.BodyBytes = int64(mib) * 1024 * 1024
	return c, nil
}
