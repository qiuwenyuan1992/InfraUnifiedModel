import { expect, it, vi } from "vitest"

const registered = vi.hoisted(() => [] as string[])
vi.mock("./App.vue", () => ({ default: {} }))
vi.mock("vue", async importOriginal => {
  const actual = await importOriginal<typeof import("vue")>()
  const app = { component: (name: string) => registered.push(name), use: () => app, mount: vi.fn() }
  return { ...actual, createApp: () => app }
})

it("启动注册拓扑定位所需的输入框，不能渲染成未知自定义元素", async () => {
  await import("./main")
  expect(registered).toContain("ElInput")
  expect(registered).toContain("ElSelect")
})
