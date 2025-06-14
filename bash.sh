# 安装 Wine
brew install --cask wine-stable

# 安装 Winetricks（管理 Wine 环境）
brew install winetricks

# 设置干净的 Wine 环境
mkdir ~/wine-test
cd ~/wine-test
WINEARCH=win64 WINEPREFIX=~/wine-test winecfg
winetricks corefonts  # 安装核心字体

# 验证 EXE 文件
wine /Users/huangfu/Downloads/XTimer.exe