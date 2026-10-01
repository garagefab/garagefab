- Geliştirici, bu projeyi geliştirirken AI agent larla yapacağı sobhetleri ve kendi oluşturduğu dosyaları Türkçe yazabilir. Fakat sohbet ettiği agent, bir dosya oluşturması veya değişiklik yapması istenmiş ise, bunu İngilizce olarak yazacaktır.

- Software factory süreci temel olarak PROJECT_DOCS/research/Claude_Academy_The_AI_Native_SDLC_Playbook.pdf anlatılanları uygulayacak, fakat bu gereksinimler detaylı analiz edilecek.

- Gereksinimler belirlenirken PROJECT_DOCS/research/ dizinindeki diğer dokümanlardan da faydalanılacak.

- garagefab geliştirilirken de sanki garagefab bütün özellikleri ile bitmiş bir uygulama olarak düşünülecek, ve her geliştirme adımında garagefab prensipleri manuel olarak uygulanmaya çalışılacak. Bu sayede, her aşamada karşılaşılan sorunları belirleyebilir, daha iyi nasıl yapılabileceğini görür ve garagefab geliştirmesi için bunları bulgu ve iyileştirme olarak kullanabiliriz.

- Nihai olarak hedef, The_AI_Native_SDLC_Playbook.pdf dokümanında anlatılan "AI Native SDLC" prensipleri ile işleyen bir Software Factory inşa etmek. Fakat bahsedilen Claude araçlarına bağımlı olmayacağız. Kullanıcı mümkün olan her yerde kendi tercih ettiği araçları kullanabilecek. Örneğin coding agent olarak Codex, Claude Code, Antigravity CLI, Pi, Opencode vb. istediğini kullanabilir. Veya iş takibi için Github Issues yerine JIRA kullanabilecek, Garagefab bunlarla birlikte çalışabilecek.

- Sistem Mimarisi (Orchestrator - Workers Modeli):
Garagefab, merkezi bir Orchestrator ve ona bağlı çalışan iki tip Worker mimarisiyle çalışacaktır:
  1. AI-Driven Workers (Yapay Zeka CLI Ajanları): Akıl yürütme, gereksinim analizi, planlama, kodlama ve kod inceleme gibi zeka gerektiren tüm aşamaları yürütür (Claude Code, OpenAI Codex, Antigravity CLI, Pi vb.). Orchestrator tarafından tahsis eiln izole Git worktree içinde çalışır ve iş bittiğinde gerekli çıktılarını üretir.
  2. Deterministic Workers: testleri çalıştırmak, build etmek gibi komut satırından çalıştırılabilecek işleri doğrudan ve sıfır maliyetle koşan araçlar. Testleri çalıştırmak örneğinde, ajanların testleri gevşetmesini veya manipüle etmesini önlemek için kodlama adımından sonra bağımsız bir kontrol kapısı olarak koşabilir. Entegre Döngü (Agent-Runner Feedback Loop) önerisi: CLI Ajanı kodu yazar -> Deterministik Koşucu testi nesnel olarak doğrular -> Test patlarsa, Orchestrator hata çıktısını ve varsa diğer gerekli bilgileri alıp otomatik olarak CLI Ajanına geri iletir (Loopback) ve düzeltilmesini ister.

- Ana fikir olarak The_AI_Native_SDLC_Playbook içindeki şu ifadeyi alabiliriz:
"What is an AI-native SDLC?
The AI-native SDLC is a reimagined process that combines the old control objectives with new enforcement. Instead of a linear flow, the process becomes a loop, and AI is embedded at each point. The AI-native SDLC promotes automated handover and triggering of subsequent plays, helping to address the manual and clunky nature of handoff between the phases of the traditional SDLC."

- Ana akış olarak The_AI_Native_SDLC_Playbook içindeki "How the plays work" başlığında anlatılanlar kullanılacak. Adımlarda yapılacak işler ise daha sonraki bölümlerde anlatılmakta: "Capture as intent.md", "Requirements and design" gibi. 

- "Continuous evals in CI" başlığı incelenerek eval yapısı sisteme dahil edilsin mi karar verilecek.

- Her bir adımda agent ın ihtiyaç duyacağı skill ler belirlenmiş olacak ve agent a input olarak söylenecek.

- garagefab AI coding agent'larını (Codex, Claude Code, Antigravity CLI, Pi, Opencode vb.) denetimli ve tekrarlanabilir bir şekilde çalıştırabilecek.

- Üzerinde çalışılan projeler tanımlandıktan sonra, her bir projede kaydedilen intent.md dosyaları ilk adımı çalıştıracak şekilde süreci tetikleyecek. Bir işin başlangıç noktası intent.md kaydedilmesi olabileceği gibi, Task sisteminden intent tipinde bir iş girilmesi veya bir bug report un geliştirmeye alınması için onaylanması da olabilir (Github issue, JIRA ticket vb.)

- Ana süreç bir yazılım fabrikası (software factory). Süreçte hangi adımlar olduğu, hangi adımda agent ne yapacak, human ne yapacak, girdiler ve çıktılar nelerdir, agent çalıştı ise bütün agent logları kaydedilecek.

- garagefab süreç adımlarına göre takip ettiği iş kuyruklarında bekleyen işleri her sürecin tanımlı iş akışına göre çalıştıracak. Bir adımdaki iş tamamlandıktan sonra ya sonraki adıma geçecek, ya da süreçte tanımlı kurallara göre önceki bir adıma döndürülecek, örneğin Testlerin çalıştırılması adımında testler geçmezse Build adımına geri döndürülmesi gibi. Kullanıcı önyüzden her aşamada hangi işlerin beklediğini, tamamladığını görecek, işlerle ilgili sonuçları ve artifact'ları görebilecek.

- Bir süreç adımı tamamlandığında otomatik olarak sonraki adıma geçilebildiği gibi, human review bekliyor statüsüne de alınabilir. Bu durumda da kullanıcı o işi istediği coding agent penceresinde açıp üzerinde çalışabilir.

- agent iş yaparken git worktrees kullanacak. Başka alternatifler de daha sonra eklenebilir, örneğin db, cache, etc. her şey o anki iş için isolated olsun isteniyorsa bir vm ie docker instance açılması gibi.

- between steps, agent can suggest some rule need to be added to AGENTS.md or that the algorith of the step to be modified. If it thinks so, it records on the Issue Tracker so that human can see and evaluate. This can be a "Agent Önerisi" type issue. Also, put some mechanism in place so that the agent can complain about something. It may make the system better if agent is given more freedom or access to more information. This can be a "Agent Complaint" type issue.

- proje geliştirilirken github actions, github issues ve github ın sunduğu diğer free servisler kullanılacak. Bu servisler garagefab çalışırken de kullanılabilecek, veya kullanıcı istediği başka sistemleri bağlayacak (Örneğin github yerine gitlab, veya github issues yerine JIRA)

- Software Factory sürecinde "Tek bir süper-ajan" yerine "Görev Başına Tek Uzman Ajan" (One Agent per Task) yaklaşımı uygulanacak: Süreç her aşamada bağımsız ve sınırları net çizilmiş ajanlarla yürütülecek (ör. Triage/Sınıflandırma Ajanı, Analiz/Spec Ajanı, Kodlama Ajanı, Bağımsız İnceleme Ajanı).

- Koda başlamadan önce sorunu kanıtlayan test (Failing Probe / Repro) adımı: Ajan bir hatayı düzeltmeye veya yeni bir özellik eklemeye başlamadan önce, mevcut ana dalda (main) o hatanın gerçekten var olduğunu veya özelliğin eksik olduğunu kanıtlayan başarısız bir test/kod parçası ("failing probe") üretecek ve çalıştırma kanıtını kaydedecek. Hata repro edilmeden kodlama adımına geçilmeyecek.

- "İnceleme Tiyatrosu"nu (Reviewer Theater) önlemek için Bağımsız Reviewer Ajanı: Kodu yazan ajan ile kodu inceleyen ajan birbirinden tamamen izole olacak (aynı bağlamı paylaşmayacak). İnceleme ajanı; yan etki riski, performans riski ve geriye dönük uyumluluk (backward compatibility) risklerini bağımsız olarak puanlayıp raporlayacak.

- İnsan Onayına "Kanıt Zinciri" (Chain of Evidence) sunulması: Onay kapısında (Approval Gate) bekleyen insanın hızlı ve güvenle karar verebilmesi için önüne sadece ham Git diff'i değil; repro testinin sonucu, yerel test logları ve bağımsız review ajanının risk puanlarını içeren yapılandırılmış bir kanıt paketi sunulacak.

- Fabrikanın Hatalardan Öğrenerek İyileşmesi (Continuous Factory Tuning): Süreçte başarısız olan veya insana devredilen her iş bir geri bildirim (telemetri) olarak toplanacak. Bu veriler kullanılarak ajanın prompt'ları, bağlamı (context) veya `AGENTS.md` kuralları güncellenecek; böylece fabrikanın otomasyon sınırları zamanla genişletilecek. Bu bahsedilen iş ilk aşamada human tarafından incelenecek, daha sonra iyileştirme önerisi yapan bir ajan tarafından "Agent Önerisi" tipinde bir issue oluşturulması düşünülebilir.

- Clarification Gate (Netleştirme Kapısı): Görev başlatma veya analiz aşamasında, ajan gelen talebi inceleyerek "aksiyon alınabilir" (actionable) olup olmadığını değerlendirecek. İstek belirsiz, eksik veya çelişkili ise ajan kesinlikle varsayımlarla körlemesine kod yazmaya başlamayacak; süreci "Açıklama Bekliyor" (Needs Clarification) statüsüne alacak. Kullanıcı bu işi bir coding agent ile açtığında, ajan kullanıcıya netleştirme soruları yöneltmecek. Kullanıcıdan yanıt gelene kadar sonraki adıma geçilmeyecek.

- Akıllı Proje Hafızası (Factory Brain): Her proje için o repoya özel kalıcı bir hafıza tutulacak. Bu hafızada; sık yapılan hatalar ve geçmişte çözülen tuzaklar gibi kayıtlar kaydedilecek. Agent lar "Agent History ekleme önerisi" tipinde bir issue açarak önerebilirler, veya kullanıcı kendisi bir kayıt ekleyebilir. Factory Brain iki katmanlı çalışacak: Projenin derleme, mimari ve kritik tuzaklarına dair damıtılmış altın kurallar (Core Facts) her görevde ajana zorunlu girdi (Push) olarak verilecek. Geçmiş hata analizleri ve detaylı çözüm kayıtları ise ajanın yalnızca ihtiyaç duyduğunda inceleyebileceği erişilebilir bir derin arşiv (Pull) olarak bulunacak.

- Akıllı Proje Hafızası (Factory Brain) Mimarisi ve Depolama:
Tek Gerçek Kaynağı (Filesystem-First & Git-Backed): Hafıza harici bir vektör veritabanında (black-box) değil; doğrudan projenin içinde Git ile versiyonlanan Markdown dosyalarında tutulacak (Örneğin `AGENTS.md` ve `PROJECT_DOCS/brain/` altındaki `gotchas.md`, `architecture.md`, `build_and_test.md` gibi). Böylece hafıza insan tarafından kolayca okunabilecek, `git diff` ile denetlenebilecek ve PR açılarak düzenlenebilecek.
Sıfır Harici Bağımlılıkla Yerel İndeksleme: Öneri: Garagefab çekirdeği, bu Markdown dosyalarını gömülü SQLite veritabanı üzerindeki FTS5 (Full-Text Search) modülüyle yerel olarak indeksleyecek; hiçbir harici sunucu veya embedding API maliyeti olmadan çalışacak.
Akıllı ve Alakalı Enjeksiyon (Smart Injection): Yeni bir görev başladığında, Orchestrator gerek görürse, görev başlığı ve dosya yollarıyla eşleşen en kritik 3-5 kuralı/tuzağı bu indeksten şimşek hızında çekecek ve ajanın sistem prompt'una kompakt bir hap bilgi özeti olarak iliştirecek (context şişmesi engellenecek).

- Yapılandırılabilir Onay Kapıları (Configurable Approval Gates): Süreçteki her adımın öncesine veya sonrasına isteğe bağlı olarak insan onay kapısı (Human-in-the-Loop) tanımlanabilmelidir (ör. `approval = "before"` veya `approval = "after"`). Düşük riskli adımlar otomatik olarak akabilmeli; riskli adımlarda (kod push etme, PR açma vb.) sistem duraklayıp kullanıcıdan onay beklemelidir.

- Adım İçi "Doğrula ve Onar" Döngüsü (Validate-and-Repair Loop): Bir adımda çalışan ajan bir çıktı ürettiğinde (kod, test veya spec), bu çıktı derhal doğrulama testlerinden veya linter kontrollerinden geçirilecek. Hata alınırsa süreç hemen durdurulmayacak; ajan hata çıktısını girdi olarak alarak aynı adım içinde kendi ürettiği hatayı belirli bir deneme sınırıyla (ör. max 3 deneme) otomatik olarak tamir etmeye (repair) çalışacaktır. Vercel AI SDK (https://github.com/vercel/ai) içinde yer alan ToolLoopAgent ve yeni iş akışı (workflow) mekanizmaları kod seviyesinde çok temiz modeller olduğu için, implementasyon sırasında referans kod tabanı olarak alınabilir.

- Süreç Takibi ve Geçici Fabrika Dashboard'u Olarak GitHub Projects (Öneri / Dogfooding):
  (Not: Bu başlık altındaki maddeler şimdilik birer mimari ve süreç önerisi niteliğindedir. Garagefab'ın nihai spec ve PRD aşamalarında AI ajanları tarafından detaylıca incelenerek karara bağlanacaktır.)
  Garagefab'ın kendi yerleşik web arayüzü ve durum makinesi (state machine) geliştirilene kadar; fabrikanın SDLC yaşam döngüsünü bizzat deneyimlemek (dogfooding) ve iş akışını şeffaf şekilde yönetmek için GitHub Projects kullanımı değerlendirilebilir. Bu pano, hem geliştirme sürecinde geçici bir canlı durum panosu (dashboard) işlevi görebilir hem de Garagefab Orchestrator ileride destekleyebileceği dış iş takip sağlayıcıları (GitHub Provider) için referans mimari teşkil edebilir.

-- GitHub Projects Süreç Fazları ve Sütun Yapısı (Öneri Durum Makinesi):
  İşlerin (Issue/Task), GitHub Projects üzerinde Garagefab ilkelerini manuel veya yarı-otomatik koşturacak şekilde aşağıdaki gibi taslak aşamalar arasında ilerletilmesi düşünülebilir:
  1. 01_Intent (Ham Niyet / Backlog): Görevin veya özelliğin ilk girdiği ham niyet havuzu.
  2. 02_Clarification_and_Spec (Netleştirme ve Analiz): Ajanın gereksinimi netleştirdiği, soru sorduğu ve spec dokümanını hazırlayabileceği aşama.
  3. 03_Failing_Probe (Repro Testi): Koda dokunmadan önce hatayı/eksikliği kanıtlayan başarısız testin oluşturulup doğrulandığı aşama.
  4. 04_Coding (Kodlama / Worktree): İzole Git worktree içinde ilgili ajanın kodu ve testleri yazdığı aşama.
  5. 05_Independent_Review (Bağımsız İnceleme): Kodu yazandan izole bağımsız inceleme ajanının risk puanı ve analiz raporu üretebileceği aşama.
  6. 06_Human_Approval_Gate (İnsan Onay Kapısı): Kanıt zincirinin (diff, test logları, risk puanı) insana sunulduğu ve onay beklenebilecek duraklama kapısı.
  7. 07_Done (Tamamlandı): PR'ın ana dala (main) merge edilip işin kapatıldığı durum.

-- GitHub Projects Özel Alanları ile Ajan ve Süreç Telemetrisi (Öneri):
  Klasik ToDo/Done takibinin ötesinde, fabrikanın yürütme metriklerini izlemek adına GitHub Projects'in özel alanlarından (Custom Fields) faydalanılması değerlendirilebilir. Örnek alanlar:
  - `Assigned Worker` (Single Select): İşi yürüten ajan türü (Claude Code, OpenAI Codex, Antigravity CLI, Human vb.).
  - `Approval Gate Status` (Single Select): Kapı durumu (`None`, `Needs Clarification`, `Pending Human Approval`, `Approved`).
  - `Review Risk Score` (Single Select / Number): İnceleme ajanının ürettiği risk seviyesi (`Low`, `Medium`, `High`).
  - `Repair Loop Count` (Number): Adım içi "Doğrula ve Onar" (Validate-and-Repair) döngüsünün tetiklenme sayısı (0..3).

-- Garagefab Orchestrator İçin Referans Sağlayıcı Değerlendirmesi (GitHub GraphQL API):
  GitHub Projects'in GraphQL API'ı ve GitHub Actions entegrasyonu, Garagefab çekirdeğinin harici iş yönetim sistemleriyle nasıl konuşacağına dair birincil referans entegrasyon (GitHub Provider) adayı olarak incelenebilir. Böylece Garagefab'ın ileride kartları programatik olarak taşıyabilmesi, durumları ve custom field'ları API üzerinden otomatik güncelleyebilmesi mümkün kılınabilir.

------

Daha sonra incelenecek konular (Birinci fazın tamamlanmasını çok uzatabileceği için ilk faza alınmayan, fakat daha sonra eklenmesinin faydalı olabileceği düşünülen konular):
- data lake, context lake, spotify Backstage internal developer platform faydalı mı? garagefab içine koyabilir miyiz, araştırılacak.
- Agent PR oluşturduktan sonra şöyle bir adım koyabilir miyiz: if you think this is a slop pr (eğer agent review sonucu skoru düşükse), use this as the spec and if you are going to implement from scratch, how would you redo it?
- How_Software_Factories_Improve-Themselves: An outer-loop agent can inspect a triage agent's work and propose updates to its skill through a human-reviewed pull request.
- Self-Healing CI (Otomatik CI Onarımı): Ajan PR oluşturduktan sonra GitHub Actions veya harici CI kontrolleri kırmızıya dönerse (test, lint veya derleme başarısız olursa), garagefab bunu otomatik olarak algılayacak. Başarısız CI loglarını çekerek ajana iletecek; ajan hatayı teşhis edip aynı PR branch'ine otomatik düzeltme commit'i atacak. Sonsuz döngüleri ve maliyet patlamasını engellemek için CI onarımına katı bir bütçe (örneğin en fazla 3 düzeltme denemesi) konulacak; limit aşılırsa iş "İnsan İncelemesi Bekliyor" statüsüne alınacak.