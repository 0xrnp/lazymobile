--- lazymobile in a floating terminal. `:Lazymobile` opens it, and again hides it while it keeps
--- running, so a log stream is still there when it comes back. `q` in lazymobile closes it.
local M = {}

M.config = {
  -- The command to run. Add flags here, such as "--keep-simulator-app".
  cmd = { "lazymobile" },
  -- The window's share of the editor, from 0 to 1.
  width = 0.9,
  height = 0.9,
  border = "rounded",
}

local state = { buf = nil, win = nil }

---@param opts? table
function M.setup(opts)
  M.config = vim.tbl_deep_extend("force", M.config, opts or {})
end

local function window_config()
  local columns, lines = vim.o.columns, vim.o.lines - vim.o.cmdheight
  local width = math.max(math.floor(columns * M.config.width), 1)
  local height = math.max(math.floor(lines * M.config.height), 1)
  return {
    relative = "editor",
    style = "minimal",
    border = M.config.border,
    width = width,
    height = height,
    row = math.floor((lines - height) / 2),
    col = math.floor((columns - width) / 2),
  }
end

local function is_open()
  return state.win ~= nil and vim.api.nvim_win_is_valid(state.win)
end

local function hide()
  if is_open() then
    vim.api.nvim_win_close(state.win, true)
  end
  state.win = nil
end

local function show()
  state.win = vim.api.nvim_open_win(state.buf, true, window_config())
  vim.cmd.startinsert()
end

local function start()
  local exe = M.config.cmd[1]
  if vim.fn.executable(exe) ~= 1 then
    vim.notify(
      exe .. " was not found. Install it: brew install 0xrnp/tap/lazymobile",
      vim.log.levels.ERROR,
      { title = "lazymobile" }
    )
    return
  end
  state.buf = vim.api.nvim_create_buf(false, true)
  show()
  local buf = state.buf
  local function on_exit(_, code)
    vim.schedule(function()
      if state.buf == buf then
        state.buf = nil
        -- A failure stays on screen so its message can be read. The buffer goes when its window
        -- closes, and the next :Lazymobile starts afresh.
        if code ~= 0 and is_open() and vim.api.nvim_buf_is_valid(buf) then
          vim.bo[buf].bufhidden = "wipe"
          vim.notify("lazymobile exited with code " .. code, vim.log.levels.WARN, { title = "lazymobile" })
          return
        end
        hide()
      end
      if vim.api.nvim_buf_is_valid(buf) then
        vim.api.nvim_buf_delete(buf, { force = true })
      end
    end)
  end
  if vim.fn.has("nvim-0.11") == 1 then
    vim.fn.jobstart(M.config.cmd, { term = true, on_exit = on_exit })
  else
    vim.fn.termopen(M.config.cmd, { on_exit = on_exit })
  end
end

--- Open lazymobile, or hide it when it is open.
function M.toggle()
  if is_open() then
    return hide()
  end
  if state.buf and vim.api.nvim_buf_is_valid(state.buf) then
    return show()
  end
  start()
end

vim.api.nvim_create_autocmd("VimResized", {
  group = vim.api.nvim_create_augroup("lazymobile", { clear = true }),
  callback = function()
    if is_open() then
      vim.api.nvim_win_set_config(state.win, window_config())
    end
  end,
})

return M
