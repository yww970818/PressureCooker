package main

import "pressurecooker/manager/internal/persistence"

func rewriteTables(query, prefix string) string { return persistence.RewriteTables(query, prefix) }
