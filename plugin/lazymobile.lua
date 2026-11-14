if vim.g.loaded_lazymobile then
  return
end
vim.g.loaded_lazymobile = true

vim.api.nvim_create_user_command("Lazymobile", function()
  require("lazymobile").toggle()
end, { desc = "Open or hide lazymobile" })
