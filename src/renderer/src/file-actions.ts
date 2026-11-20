import { loadDirectory, notify, parentPath, state, unwrap, type Workspace } from './workbench'

export async function uploadFolder(session: Workspace, directory: string): Promise<void> {
  const { uploadDirectory } = window.sshTool
  if (!uploadDirectory) return
  const id = session.id
  const tasks = unwrap(await uploadDirectory(id, directory))
  if (tasks === null) return
  if (tasks > 0) state.transferOpen = true
  else notify('远程文件夹已创建')
  if (session.id === id) await loadDirectory(session, directory, true)
}

export async function downloadFolder(session: Workspace, path: string): Promise<void> {
  const { downloadDirectory } = window.sshTool
  if (!downloadDirectory) return
  const tasks = unwrap(await downloadDirectory(session.id, path))
  if (tasks === null) return
  if (tasks > 0) state.transferOpen = true
  else notify('本地文件夹已创建')
}

export async function removeRemoteTree(session: Workspace, path: string): Promise<void> {
  const { deleteTree } = window.sshTool
  if (!deleteTree) return
  const id = session.id
  const deleted = unwrap(await deleteTree(id, path))
  if (deleted && session.id === id) await loadDirectory(session, parentPath(path), true)
}
