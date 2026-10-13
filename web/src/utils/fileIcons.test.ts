import { describe, expect, it } from 'vitest'
import { fileIcon } from './fileIcons'

describe('fileIcon', () => {
  it('entry type wins over the name', () => {
    expect(fileIcon('src', 'dir')).toBe('📁')
    expect(fileIcon('latest', 'link')).toBe('🔗')
  })

  it('picks an icon per file kind', () => {
    expect(fileIcon('main.go')).toBe('🐹')
    expect(fileIcon('app.py')).toBe('🐍')
    expect(fileIcon('index.ts')).toBe('🟦')
    expect(fileIcon('photo.png')).toBe('🖼️')
    expect(fileIcon('clip.mp4')).toBe('🎬')
    expect(fileIcon('song.mp3')).toBe('🎵')
    expect(fileIcon('backup.tar.gz')).toBe('📦')
    expect(fileIcon('report.pdf')).toBe('📕')
    expect(fileIcon('data.json')).toBe('🗃️')
    expect(fileIcon('deploy.yaml')).toBe('🔧')
    expect(fileIcon('run.sh')).toBe('🐚')
    expect(fileIcon('mystery.xyz')).toBe('📄')
  })

  it('knows extension-less and hidden files', () => {
    expect(fileIcon('Dockerfile')).toBe('🐳')
    expect(fileIcon('Dockerfile.dev')).toBe('🐳')
    expect(fileIcon('Makefile')).toBe('🛠️')
    expect(fileIcon('README.md')).toBe('📖')
    expect(fileIcon('LICENSE')).toBe('⚖️')
    expect(fileIcon('.gitignore')).toBe('🔧')
    expect(fileIcon('.env.local')).toBe('🔧')
    expect(fileIcon('package-lock.json')).toBe('🔒')
  })

  it('matches on basename, case-insensitively', () => {
    expect(fileIcon('src/deep/Photo.JPG')).toBe('🖼️')
    expect(fileIcon('docs/README')).toBe('📖')
  })

  it('ignores Object.prototype members as names', () => {
    expect(fileIcon('constructor')).toBe('📄')
    expect(fileIcon('x.constructor')).toBe('📄')
    expect(fileIcon('.constructor')).toBe('📄')
    expect(fileIcon('__proto__')).toBe('📄')
    expect(fileIcon('x.__proto__')).toBe('📄')
  })
})
