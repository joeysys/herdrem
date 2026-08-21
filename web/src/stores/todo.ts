import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ws } from '../api/ws'
import type { Todo, TodoBinding, TodoSet } from '../api/types'

export const useTodoStore = defineStore('todo', () => {
  const todos = ref<Record<string, Todo>>({})

  function setAll(items: Todo[]) {
    todos.value = Object.fromEntries(items.map((item) => [item.id, item]))
  }

  function updateItem(item: Todo, deleted = false) {
    if (deleted) delete todos.value[item.id]
    else todos.value[item.id] = item
  }

  function setItem(item: Pick<Todo, 'body' | 'binding'> & { id?: string }) {
    const msg: TodoSet = { type: 'todo_set', id: item.id, body: item.body, binding: item.binding }
    ws.send(msg)
  }

  function deleteItem(id: string) {
    const msg: TodoSet = { type: 'todo_set', id, body: '' }
    ws.send(msg)
  }

  function hasItems(binding: TodoBinding): boolean {
    return Object.values(todos.value).some((item) => sameBinding(item.binding, binding))
  }

  function itemsFor(binding: TodoBinding): Todo[] {
    return Object.values(todos.value).filter((item) => sameBinding(item.binding, binding))
  }

  function addItem(binding: TodoBinding | undefined, body: string) {
    setItem({ body, binding })
  }

  return { todos, setAll, updateItem, setItem, deleteItem, hasItems, itemsFor, addItem }
})

export function sameBinding(a?: TodoBinding, b?: TodoBinding): boolean {
  return !!a && !!b && a.host === b.host && a.workspace_id === b.workspace_id
}
