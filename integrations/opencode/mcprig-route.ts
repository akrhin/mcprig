import { Plugin } from "@opencode/plugin"

/**
 * Route hint for agents that reach MCP servers through mcprig.
 *
 * Deliberately generic: only mcprig's own meta-tools are named. The servers
 * behind the proxy never appear here, so the same file works for any set, on
 * any host, and stays correct when a set changes.
 *
 * The text is constant on purpose: it is appended to the system prompt on every
 * step, and any variable part (time, session id, server count) would change the
 * request prefix each turn and throw the prompt cache away.
 */
const HINT =
  "Если в тулах есть `mcprig`: MCP-серверы проекта доступны только через него — " +
  "внутри execute вызывай `tools[\"mcprig\"]`: list_servers (что настроено), " +
  "list_commands (какие инструменты есть), describe_commands (схема аргументов), " +
  "invoke_command (сам вызов)."

export default Plugin.define({
  id: "mcprig.route",
  async setup(ctx) {
    // Выключение без правки файла: MCPRIG_ROUTE=off в окружении хоста.
    if (process.env.MCPRIG_ROUTE === "off") {
      return
    }
    // `context` — обычный чат, `compaction` — запрос сжатия: без второго хука
    // правило теряется ровно на том запросе, который переписывает контекст.
    const addHint = (event) => {
      event.system.push({ type: "text", text: HINT })
    }
    await ctx.session.hook("context", addHint)
    await ctx.session.hook("compaction", addHint)
  },
})
