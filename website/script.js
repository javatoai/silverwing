(() => {
  const header = document.querySelector('[data-header]');
  const menuToggle = document.querySelector('[data-menu-toggle]');
  const mobileMenu = document.querySelector('[data-mobile-menu]');
  const modal = document.querySelector('[data-lightbox-modal]');
  const modalImage = document.querySelector('[data-lightbox-image]');
  const modalCaption = document.querySelector('[data-lightbox-caption]');
  const toast = document.querySelector('[data-toast]');

  const setHeaderState = () => header?.classList.toggle('is-scrolled', window.scrollY > 18);
  setHeaderState();
  window.addEventListener('scroll', setHeaderState, { passive: true });

  const closeMenu = () => {
    mobileMenu?.classList.remove('is-open');
    mobileMenu?.setAttribute('aria-hidden', 'true');
    menuToggle?.setAttribute('aria-expanded', 'false');
  };
  menuToggle?.addEventListener('click', () => {
    const open = !mobileMenu.classList.contains('is-open');
    mobileMenu.classList.toggle('is-open', open);
    mobileMenu.setAttribute('aria-hidden', String(!open));
    menuToggle.setAttribute('aria-expanded', String(open));
  });
  mobileMenu?.querySelectorAll('a').forEach(link => link.addEventListener('click', closeMenu));

  const observer = new IntersectionObserver(entries => {
    entries.forEach(entry => {
      if (entry.isIntersecting) {
        entry.target.classList.add('is-visible');
        observer.unobserve(entry.target);
      }
    });
  }, { threshold: .12 });
  document.querySelectorAll('.reveal').forEach(node => observer.observe(node));

  const closeLightbox = () => {
    modal?.classList.remove('is-open');
    modal?.setAttribute('aria-hidden', 'true');
    if (modalImage) modalImage.src = '';
    document.body.style.overflow = '';
  };
  const openLightbox = (button) => {
    const file = button.dataset.lightbox;
    if (!file || !modal || !modalImage) return;
    modalImage.src = `./assets/screenshots/${file}`;
    modalImage.alt = button.querySelector('img')?.alt || 'silverwing 界面截图';
    if (modalCaption) modalCaption.textContent = button.dataset.caption || '';
    modal.classList.add('is-open');
    modal.setAttribute('aria-hidden', 'false');
    document.body.style.overflow = 'hidden';
  };
  document.querySelectorAll('[data-lightbox]').forEach(button => button.addEventListener('click', () => openLightbox(button)));
  modal?.querySelectorAll('[data-lightbox-close]').forEach(node => node.addEventListener('click', closeLightbox));
  document.addEventListener('keydown', event => { if (event.key === 'Escape') { closeLightbox(); closeMenu(); } });

  let toastTimer;
  const showToast = (message) => {
    if (!toast) return;
    toast.textContent = message;
    toast.classList.add('is-visible');
    window.clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => toast.classList.remove('is-visible'), 1800);
  };
  document.querySelectorAll('[data-copy]').forEach(button => button.addEventListener('click', async () => {
    const value = button.dataset.copy;
    try {
      await navigator.clipboard.writeText(value);
      showToast('命令已复制到剪贴板');
    } catch {
      showToast(value);
    }
  }));
})();
