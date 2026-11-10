package main

func collectionEnvironmentSpecs() []managerSpec {
	props := func() map[string]any {
		p := environmentProperties()
		p["collectionId"] = projectSchema()
		p["collectionEnvironmentId"] = projectSchema()
		p["parent_environment_id"] = environmentIDSchema()
		p["revision"] = revisionSchema()
		return p
	}
	return []managerSpec{
		{name: "list_api_collection_environments", description: "列出当前集合专属环境；旧覆盖仅在新字段缺失时投影，包含父环境可用状态。", schema: scopedSchema(map[string]any{"collectionId": projectSchema()}, "collectionId"), readOnly: true},
		{name: "get_api_collection_environment", description: "读取集合环境自有配置与当前父项目环境合并的生效值。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "collectionEnvironmentId": projectSchema()}, "collectionId", "collectionEnvironmentId"), readOnly: true},
		{name: "create_api_collection_environment", description: "按集合当前revision创建独立环境或持续继承父项目环境；ID由管理端生成。独立环境必须提供base_host，变量空字符串有效。", schema: scopedSchema(props(), "collectionId", "revision", "name")},
		{name: "update_api_collection_environment", description: "按集合当前revision替换指定集合环境的自有配置；省略base_host恢复父地址继承，省略父ID解除继承时须提供base_host。", schema: scopedSchema(props(), "collectionId", "collectionEnvironmentId", "revision", "name")},
		{name: "delete_api_collection_environment", description: "按集合当前revision删除集合环境；历史集合修订与已提交任务快照保留，不自动选择其他环境。", schema: scopedSchema(map[string]any{"collectionId": projectSchema(), "collectionEnvironmentId": projectSchema(), "revision": revisionSchema()}, "collectionId", "collectionEnvironmentId", "revision")},
	}
}
