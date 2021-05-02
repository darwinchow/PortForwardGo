@echo off
set UserName=CoiaPrant
set Name=PortForwardGo

if exist dist (
    del /F /S /Q dist
    rd /S /Q dist
)

if not exist .git (
    git init
    git config --global core.autocrlf true
    git config --global credential.helper store
    git remote add origin https://github.com/%UserName%/%Name%.git
)

git add --all
git commit -m "update file"
git push -u origin master
pause