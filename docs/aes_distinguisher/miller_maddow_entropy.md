# Finite-Sample Miller-Madow Bias Correction & Multi-Dimensional Entropy Estimation

**Module**: `docs/aes_distinguisher/miller_maddow_entropy.md`  
**Target Codebase**: `https://github.com/lemon4ksan/przm`  
**Related Source Files**:
- Go Implementation: `pkg/aes_distinguisher/entropy.go`
- C Reference: `src/aes_distinguisher.c`, `include/aes_distinguisher.h`
- Benchmark Suite: `pkg/aes_distinguisher/entropy_test.go`  
**Classification**: Information Theory & Statistical Cryptanalysis

---

## 1. Introduction & The Finite-Sample Entropy Estimation Problem

Shannon entropy quantifies the fundamental limit of compressibility and information content in discrete stochastic processes. In cryptanalysis and statistical testing, empirical entropy estimation is commonly employed to evaluate whether an intermediate transformation or output stream of a cryptographic primitive behaves indistinguishably from an ideal random source (independent and identically distributed uniform white noise).

However, empirical entropy estimation in finite sample regimes is subject to severe statistical bias. Without analytical debiasing, finite sample artifacts can masquerade as cryptographic vulnerabilities or cause false-positive distinguisher triggers.

### 1.1 Shannon Entropy Definition

Let $X$ be a discrete random variable taking values in a finite alphabet $\mathcal{X} = \{0, 1, \dots, K-1\}$ of cardinality $K = |\mathcal{X}|$. For byte-oriented cryptographic streams, $\mathcal{X} = \{0, 1, \dots, 255\}$ and $K = 256$.

Let $p = (p_0, p_1, \dots, p_{K-1})$ denote the true underlying probability distribution over $\mathcal{X}$, where $p_i = \mathbb{P}(X = i) \ge 0$ and $\sum_{i=0}^{K-1} p_i = 1$. The **Shannon entropy** of $X$ (measured in bits) is defined as:

$$H(X) = -\sum_{i=0}^{K-1} p_i \log_2 p_i$$

with the standard analytical convention that $0 \log_2 0 = \lim_{x \to 0^+} x \log_2 x = 0$.

When natural logarithms are used, the entropy is measured in **nats**:
$$H_{\text{nats}}(X) = -\sum_{i=0}^{K-1} p_i \ln p_i = H(X) \cdot \ln 2$$

Under the discrete uniform distribution $U_K$ where $p_i = 1/K = 1/256$, the entropy achieves its maximal theoretical bound:
$$H_{\max} = \log_2(256) = 8.0 \text{ bits/byte} \quad (8 \ln 2 \text{ nats})$$

### 1.2 The Empirical Maximum Likelihood (Plug-In) Estimator

In practical applications, the true distribution $p$ is unknown. Given an empirical sample sequence $D = (x_1, x_2, \dots, x_N) \in \mathcal{X}^N$ of size $N$, let $C_i$ denote the observed frequency of symbol $i$:
$$C_i = \sum_{j=1}^N \mathbf{1}_{\{x_j = i\}}, \quad \sum_{i=0}^{K-1} C_i = N$$

The sample counts follow a multinomial distribution:
$$(C_0, C_1, \dots, C_{K-1}) \sim \text{Multinomial}(N, p)$$
with marginal expectations $\mathbb{E}[C_i] = N p_i$ and covariance structure:
$$\text{Var}(C_i) = N p_i (1 - p_i), \quad \text{Cov}(C_i, C_j) = -N p_i p_j \quad (i \neq j)$$

The maximum likelihood estimator (empirical plug-in estimator) $\hat{p}_i$ is:
$$\hat{p}_i = \frac{C_i}{N}$$
Substituting $\hat{p}_i$ into the definition of Shannon entropy yields the **naive plug-in entropy estimator**:
$$\hat{H} = -\sum_{i=0}^{K-1} \hat{p}_i \log_2 \hat{p}_i$$

### 1.3 Systematic Downward Bias via Jensen's Inequality

The plug-in estimator $\hat{H}$ is **strictly biased downward** for any finite sample size $N < \infty$.

**Theorem 1 (Negative Bias of Empirical Entropy)**:  
*For any discrete distribution $p$ with support size $K \ge 2$ and any finite sample size $N$, the expectation of the empirical plug-in entropy estimator is strictly less than the true entropy:*
$$\mathbb{E}[\hat{H}] < H(X)$$

**Proof**:  
Consider the univariate function $g(x) = x \ln x$ defined on $[0, 1]$, with $g(0) = 0$.
The second derivative of $g(x)$ for $x \in (0, 1]$ is:
$$g'(x) = 1 + \ln x, \quad g''(x) = \frac{1}{x} > 0$$
Since $g''(x) > 0$ strictly on $(0, 1]$, $g(x)$ is strictly convex.

By **Jensen's inequality**, for any non-degenerate random variable $Y$ taking values in $[0, 1]$:
$$\mathbb{E}[g(Y)] > g\big(\mathbb{E}[Y]\big)$$
Applying this to the empirical frequency $Y = \hat{p}_i = C_i / N$:
Notice that $\mathbb{E}[\hat{p}_i] = p_i$. Unless $p_i \in \{0, 1\}$ (a degenerate deterministic distribution), $\hat{p}_i$ is a non-degenerate random variable under finite $N$. Therefore:
$$\mathbb{E}[\hat{p}_i \ln \hat{p}_i] > p_i \ln p_i$$
Multiplying by $-1$ and summing over all $i \in \{0, \dots, K-1\}$:
$$\mathbb{E}[\hat{H}_{\text{nats}}] = -\sum_{i=0}^{K-1} \mathbb{E}[\hat{p}_i \ln \hat{p}_i] < -\sum_{i=0}^{K-1} p_i \ln p_i = H_{\text{nats}}(X)$$
Dividing by $\ln 2$ yields $\mathbb{E}[\hat{H}] < H(X)$. $\blacksquare$

### 1.4 Cryptanalytic Consequence of Naive Plug-In Estimation

When analyzing a cipher block output of size $N = 4,096$ bytes (256 blocks of AES), the naive downward bias under a perfectly uniform source is:
$$\text{Bias} \approx \frac{256 - 1}{2 \times 4096 \times \ln 2} \approx \frac{255}{5678.26} \approx 0.0449 \text{ bits/byte}$$
This creates an apparent entropy deficit of:
$$\Delta H\% = \frac{0.0449}{8.0} \times 100\% = 0.561\%$$
If a statistical test applies a threshold of $\Delta H\% > 0.05\%$ to flag non-randomness, the naive plug-in estimator will trigger false alarms on 100% of pure uniform random streams. Analytical debiasing is therefore mandatory.

---

## 2. Mathematical Derivation of Miller-Maddow Debiasing

In 1955, George A. Miller and William G. Madow published an asymptotic series expansion for the expectation of the empirical entropy estimator. By evaluating the second-order Taylor expansion of $\hat{p}_i \ln \hat{p}_i$, the leading-order finite-sample bias can be evaluated analytically in closed form.

### 2.1 Taylor Series Expansion of the Entropy Function

Let $p_i > 0$ be the true probability of category $i$, and let $\epsilon_i = \hat{p}_i - p_i$ be the sampling error.
The properties of $\epsilon_i$ under multinomial sampling are:
$$\mathbb{E}[\epsilon_i] = 0$$
$$\mathbb{E}[\epsilon_i^2] = \text{Var}(\hat{p}_i) = \frac{p_i (1 - p_i)}{N}$$
$$\mathbb{E}[\epsilon_i^3] = \frac{p_i (1 - p_i)(1 - 2p_i)}{N^2} = O(N^{-2})$$

Consider the Taylor series of $g(x) = x \ln x$ around $x = p_i$:
$$g(p_i + \epsilon_i) = g(p_i) + g'(p_i) \epsilon_i + \frac{1}{2} g''(p_i) \epsilon_i^2 + \frac{1}{6} g'''(p_i) \epsilon_i^3 + O(\epsilon_i^4)$$

Evaluating the derivatives:
$$g'(p_i) = 1 + \ln p_i$$
$$g''(p_i) = \frac{1}{p_i}$$
$$g'''(p_i) = -\frac{1}{p_i^2}$$

Substituting these into the Taylor series:
$$\hat{p}_i \ln \hat{p}_i = p_i \ln p_i + (1 + \ln p_i)\epsilon_i + \frac{1}{2 p_i} \epsilon_i^2 - \frac{1}{6 p_i^2} \epsilon_i^3 + O(\epsilon_i^4)$$

### 2.2 Expectation of the Series Expansion

Taking expectations on both sides:
$$\mathbb{E}[\hat{p}_i \ln \hat{p}_i] = p_i \ln p_i + (1 + \ln p_i)\mathbb{E}[\epsilon_i] + \frac{1}{2 p_i} \mathbb{E}[\epsilon_i^2] - \frac{1}{6 p_i^2} \mathbb{E}[\epsilon_i^3] + O(N^{-2})$$
Substituting $\mathbb{E}[\epsilon_i] = 0$ and $\mathbb{E}[\epsilon_i^2] = \frac{p_i (1 - p_i)}{N}$:
$$\mathbb{E}[\hat{p}_i \ln \hat{p}_i] = p_i \ln p_i + 0 + \frac{1}{2 p_i} \left( \frac{p_i (1 - p_i)}{N} \right) + O(N^{-2})$$
$$= p_i \ln p_i + \frac{1 - p_i}{2N} + O(N^{-2})$$

### 2.3 Summation across Categories & Asymptotic Theorem

Summing over all $K$ categories $i \in \{0, 1, \dots, K-1\}$:
$$\sum_{i=0}^{K-1} \mathbb{E}[\hat{p}_i \ln \hat{p}_i] = \sum_{i=0}^{K-1} p_i \ln p_i + \sum_{i=0}^{K-1} \frac{1 - p_i}{2N} + O(N^{-2})$$
$$= -H_{\text{nats}}(X) + \frac{1}{2N} \left( \sum_{i=0}^{K-1} 1 - \sum_{i=0}^{K-1} p_i \right) + O(N^{-2})$$
Since $\sum_{i=0}^{K-1} 1 = K$ and $\sum_{i=0}^{K-1} p_i = 1$:
$$\sum_{i=0}^{K-1} \mathbb{E}[\hat{p}_i \ln \hat{p}_i] = -H_{\text{nats}}(X) + \frac{K - 1}{2N} + O(N^{-2})$$

Negating both sides yields the celebrated Miller-Madow expectation theorem:

**Theorem 2 (Miller-Madow Asymptotic Bias Theorem)**:  
*Let $X$ be a discrete random variable distributed across $K$ categories with non-zero probabilities. The expectation of the empirical plug-in entropy estimator $\hat{H}_{\text{nats}}$ satisfies:*
$$\mathbb{E}[\hat{H}_{\text{nats}}] = H_{\text{nats}}(X) - \frac{K - 1}{2N} + O(N^{-2})$$

### 2.4 Finite-Sample Miller-Madow Bias Correction Formula

Converting from nats to bits by dividing by $\ln 2$:
$$\mathbb{E}[\hat{H}] = H(X) - \frac{K - 1}{2 N \ln 2} + O(N^{-2})$$

Therefore, the leading-order finite-sample bias correction is:
$$\Delta H_{\text{MM}} = \frac{K - 1}{2 N \ln 2}$$
The **Miller-Madow bias-corrected Shannon entropy estimator** is:
$$H_{\text{corrected}} = \hat{H} + \Delta H_{\text{MM}} = -\sum_{i=0}^{K-1} \hat{p}_i \log_2 \hat{p}_i + \frac{K - 1}{2 N \ln 2}$$

### 2.5 Effective Support Size: $K_{\text{obs}}$ vs. $K$

In practical cryptographic analysis, when sample size $N$ is small relative to $K$ (e.g., $N = 128$ bytes while $K = 256$), not all $K$ categories can physically appear in the sample. If the theoretical alphabet size $K = 256$ is naively used, the term $\frac{255}{2 N \ln 2}$ would vastly over-correct, producing impossible entropy values exceeding 8.0 bits.

Miller and Madow demonstrated that when the underlying support is partially sampled, $K$ must be replaced by the **effective observed support size**:
$$K_{\text{obs}} = \sum_{i=0}^{K-1} \mathbf{1}_{\{C_i > 0\}}$$
This yields the robust operational formula implemented in PRZM:
$$\Delta H_{\text{MM}} = \begin{cases} \frac{K_{\text{obs}} - 1}{2 N \ln 2} & \text{if } N > 0 \text{ and } K_{\text{obs}} > 1 \\ 0.0 & \text{otherwise} \end{cases}$$
$$H_{\text{corrected}} = \min\big(8.0, \; \max(0.0, \; \hat{H} + \Delta H_{\text{MM}})\big)$$

---

## 3. Pearson $\chi^2$ Goodness-of-Fit Test

To complement the debiased Shannon entropy metric, PRZM incorporates the Pearson $\chi^2$ goodness-of-fit test. This test evaluates whether observed byte frequencies deviate from the uniform null hypothesis faster and with greater statistical power than scalar entropy.

### 3.1 Null Hypothesis & Test Statistic

Let $H_0$ denote the uniform null hypothesis:
$$H_0: p_i = \frac{1}{256} \quad \forall i \in \{0, 1, \dots, 255\}$$
The expected frequency in each byte bin under $H_0$ is:
$$E = \frac{N}{256}$$
The Pearson $\chi^2$ statistic is defined as:
$$\chi^2 = \sum_{b=0}^{255} \frac{(C[b] - E)^2}{E} = \sum_{b=0}^{255} \frac{(C[b] - N/256)^2}{N/256}$$

Expanding the numerator:
$$\chi^2 = \frac{256}{N} \sum_{b=0}^{255} \left( C[b]^2 - 2 C[b] \frac{N}{256} + \frac{N^2}{256^2} \right)$$
$$= \frac{256}{N} \sum_{b=0}^{255} C[b]^2 - 2 \sum_{b=0}^{255} C[b] + \frac{N}{256} \sum_{b=0}^{255} 1$$
Since $\sum_{b=0}^{255} C[b] = N$ and $\sum_{b=0}^{255} 1 = 256$:
$$\chi^2 = \frac{256}{N} \sum_{b=0}^{255} C[b]^2 - 2N + N = \frac{256}{N} \sum_{b=0}^{255} C[b]^2 - N$$

This algebraically simplified form requires only a single pass to accumulate $\sum C[b]^2$, avoiding floating-point division inside the bin iteration loop.

### 3.2 Degrees of Freedom & Distributional Parameters

Because the frequencies are constrained by $\sum_{b=0}^{255} C[b] = N$, exactly one degree of freedom is lost:
$$df = K - 1 = 256 - 1 = 255$$
Under $H_0$, the asymptotic moments of the $\chi^2$ distribution with $df = 255$ are:
$$\mathbb{E}[\chi^2] = df = 255$$
$$\text{Var}(\chi^2) = 2 \cdot df = 2 \times 255 = 510$$
$$\sigma_{\chi^2} = \sqrt{510} \approx 22.583179581272447$$

### 3.3 Normal Approximation Z-Score & P-Value Derivation

By the Central Limit Theorem, since $df = 255$ is large ($df \gg 30$), the distribution of $\chi^2$ is closely approximated by a Gaussian distribution:
$$\chi^2 \sim \mathcal{N}\big(\mu = 255, \; \sigma^2 = 510\big)$$

The standardized normal score ($Z$-score) is:
$$Z = \frac{\chi^2 - 255}{\sqrt{510}}$$

The corresponding two-tailed or upper-tailed $p$-value represents the probability of observing a $\chi^2$ value at least as extreme as the empirical statistic under $H_0$:
$$p = \mathbb{P}\big(\chi^2_{255} \ge \chi^2_{\text{obs}}\big) \approx \frac{1}{\sqrt{2\pi}} \int_Z^\infty e^{-t^2/2} dt = \frac{1}{2} \text{erfc}\left( \frac{Z}{\sqrt{2}} \right)$$
where $\text{erfc}(x) = \frac{2}{\sqrt{\pi}} \int_x^\infty e^{-t^2} dt$ is the complementary error function.

### 3.4 Distinguisher Decision Rule

The PRZM statistical engine flags a stream as non-random (distinguisher fired) if:
$$\Delta H\% > 0.05\% \quad \text{or} \quad Z > 3.29$$
A $Z$-score threshold of $Z = 3.29$ corresponds to a false positive probability under uniform noise of:
$$p = \frac{1}{2} \text{erfc}\left(\frac{3.29}{\sqrt{2}}\right) \approx 0.0005 \quad (0.05\%)$$
ensuring $99.95\%$ statistical confidence against false alarms.

---

## 4. Multi-Dimensional Entropy Extensions

Scalar byte entropy evaluates marginal 1-D distributions, which are blind to spatial correlations, sequential dependencies, or multi-byte structures. PRZM deploys three multi-dimensional entropy extensions:

### 4.1 Bigram Joint & Conditional Entropy

Let $(x_1, x_2, \dots, x_N)$ be a sequence of bytes. A consecutive bigram is a pair $(x_t, x_{t+1})$ for $t \in \{1, 2, \dots, N-1\}$.
The total number of observed bigrams is $N_{\text{bi}} = N - 1$.
The empirical joint probability distribution over the $256 \times 256 = 65,536$ state space is:
$$\hat{p}(u, v) = \frac{1}{N - 1} \sum_{t=1}^{N-1} \mathbf{1}_{\{x_t = u, \; x_{t+1} = v\}}$$

The **joint bigram entropy** $H_2(X)$ is:
$$H_2(X) = -\sum_{u=0}^{255} \sum_{v=0}^{255} \hat{p}(u, v) \log_2 \hat{p}(u, v)$$
with theoretical maximum $H_{2, \max} = \log_2(65,536) = 16.0 \text{ bits/bigram}$.

The **conditional entropy** $H(X_t \mid X_{t-1})$ measures the remaining uncertainty in byte $x_t$ given knowledge of preceding byte $x_{t-1}$:
$$H(X_t \mid X_{t-1}) = H(X_{t-1}, X_t) - H(X_{t-1}) \approx H_2(X) - H_0(X)$$
For an ideal pseudo-random permutation (PRP), $H_2(X) \to 16.0$ bits and $H(X_t \mid X_{t-1}) \to 8.0$ bits. In reduced-round AES, short-range sequential dependencies cause $H(X_t \mid X_{t-1})$ to collapse below 8.0 bits.

### 4.2 Stride-4 Column Entropy

The $4 \times 4$ AES state matrix maps column elements to byte indices separated by a stride of 4:
$$\begin{pmatrix} s_{0,c} \\ s_{1,c} \\ s_{2,c} \\ s_{3,c} \end{pmatrix} \longleftrightarrow (b_c, b_{c+4}, b_{c+8}, b_{c+12})$$
To isolate intra-column dependencies produced by the linear MixColumns transformation ($M \in \mathcal{M}_{4 \times 4}(\mathbb{F}_{2^8})$), PRZM evaluates the **stride-4 joint entropy**:
$$\hat{p}_{\text{stride4}}(u, v) = \frac{1}{N - 4} \sum_{t=1}^{N-4} \mathbf{1}_{\{x_t = u, \; x_{t+4} = v\}}$$
$$H_{\text{stride4}}(X) = -\sum_{u=0}^{255} \sum_{v=0}^{255} \hat{p}_{\text{stride4}}(u, v) \log_2 \hat{p}_{\text{stride4}}(u, v)$$
If MixColumns is omitted or incomplete (e.g., in Round 1 or Round 2), stride-4 entropy exhibits a pronounced deficit ($\Delta H_{\text{stride4}}\% \gg 0$), signaling column alignment before complete multi-column diffusion.

### 4.3 128x128 Bit-Slice Pearson Cross-Correlation Matrix

To evaluate microscopic bit-level dependencies across block cipher states, PRZM constructs the full $128 \times 128$ Pearson cross-correlation matrix across all 128 bit positions of the 16-byte state.

#### 4.3.1 Mathematical Formulation

Let $\mathcal{B} = \{B^{(1)}, B^{(2)}, \dots, B^{(M)}\}$ be an ensemble of $M$ 16-byte ciphertext blocks ($M \ge 64$).
Each block $B^{(m)}$ contains 128 bits: $b_0^{(m)}, b_1^{(m)}, \dots, b_{127}^{(m)} \in \{0, 1\}$.
For bit position $i \in \{0, \dots, 127\}$:
- The sample mean is: $\bar{b}_i = \frac{1}{M} \sum_{m=1}^M b_i^{(m)}$
- The sample variance for a binary variable is: $\sigma_i^2 = \bar{b}_i (1 - \bar{b}_i)$

For any pair of bit positions $(i, j)$ with $0 \le i < j < 128$, the **Pearson correlation coefficient** $\rho_{ij}$ is:
$$\rho_{ij} = \frac{\text{Cov}(b_i, b_j)}{\sigma_i \sigma_j} = \frac{\mathbb{E}[b_i b_j] - \bar{b}_i \bar{b}_j}{\sqrt{\bar{b}_i(1 - \bar{b}_i) \bar{b}_j(1 - \bar{b}_j)}}$$

Expressed directly in terms of integer popcounts ($S_i = \sum_{m=1}^M b_i^{(m)}$, $S_j = \sum_{m=1}^M b_j^{(m)}$, $S_{ij} = \sum_{m=1}^M b_i^{(m)} b_j^{(m)}$):
$$\rho_{ij} = \frac{M S_{ij} - S_i S_j}{\sqrt{\big(M S_i - S_i^2\big) \big(M S_j - S_j^2\big)}}$$
By definition, $\rho_{ii} = 1.0$ and $\rho_{ji} = \rho_{ij} \in [-1.0, 1.0]$.

#### 4.3.2 Total Bit Correlation Metric ($\mathcal{C}_{\text{total}}$)

The aggregate cross-correlation metric sums the squared off-diagonal coefficients across all $\binom{128}{2} = 8,128$ unique bit pairs:
$$\mathcal{C}_{\text{total}} = \sum_{0 \le i < j < 128} \rho_{ij}^2$$

- For an ideal random source, each $\rho_{ij}$ is asymptotically normal with mean 0 and variance $1/M$. Hence:
  $$\mathbb{E}[\mathcal{C}_{\text{total}}] \approx \frac{8,128}{M}$$
- When structural algebraic defects or linear invariant trails are present (e.g. 1-round to 3-round AES), specific bit pairs exhibit persistent non-zero correlation ($|\rho_{ij}| \gg 1/\sqrt{M}$), driving $\mathcal{C}_{\text{total}}$ far above the random expectation.

#### 4.3.3 SWAR 8x8 Matrix Transposition Acceleration

Extracting bit slices from an ensemble of $M$ byte blocks naively requires $128 \times M$ memory reads and bit shifts.
PRZM accelerates bit-slice extraction using the 64-bit SWAR **`przm.Transpose8x8`** primitive:
- Each 16-byte block is split into two 64-bit halves: $W_{\text{low}} = B[0..7]$ and $W_{\text{high}} = B[8..15]$.
- `Transpose8x8` performs an 8x8 bit-matrix transposition in 7 register cycles using recursive masked swaps.
- After transposition, the 8 bytes of the transposed word represent the 8 bit-planes directly!
- Bit vectors across all $M$ blocks are packed into contiguous `uint64` slices, enabling pair correlations $S_{ij} = \sum \text{popcount64}(V_i[w] \ \& \ V_j[w])$ to execute at SIMD/SWAR speed.

---

## 5. Code Mapping & API Reference

### 5.1 Go Codebase (`pkg/aes_distinguisher/entropy.go`)

- **`ComputeShannonEntropy(data []byte) (h, deltaH, deltaHPct float64)`** (lines 43–77):
  Computes raw plug-in Shannon entropy $\hat{H}$, absolute deficit $\Delta H = 8.0 - \hat{H}$, and relative deficit $\Delta H\%$.
- **`ComputeMillerMadowCorrection(data []byte) float64`** (lines 85–104):
  Calculates finite-sample bias correction $\Delta H_{\text{MM}} = \frac{K_{\text{obs}} - 1}{2 N \ln 2}$.
- **`ComputeShannonEntropyCorrected(data []byte) (hCorrected, deltaH, deltaHPct float64)`** (lines 114–135):
  Returns debiased entropy $H_{\text{corrected}} = \hat{H} + \Delta H_{\text{MM}}$, strictly clamped to $[0.0, 8.0]$.
- **`ComputeChiSquared(data []byte) (chi2, pValue, zScore float64)`** (lines 148–175):
  Evaluates Pearson $\chi^2 = \frac{256}{N}\sum C[b]^2 - N$ with $df = 255$, normal $Z = \frac{\chi^2 - 255}{\sqrt{510}}$, and asymptotic $p$-value.
- **`ComputeChiSquaredPValue(chi2 float64) float64`** (lines 179–190):
  Evaluates $p = \frac{1}{2} \text{erfc}(Z / \sqrt{2})$.
- **`ComputeBigramEntropy(data []byte) (h2, deltaH2, deltaH2Pct float64)`** (lines 194–234):
  Evaluates 65,536-bin joint bigram distribution.
- **`ComputeStride4Entropy(data []byte) (h, deltaH, deltaHPct float64)`** (lines 281–284):
  Evaluates stride-4 column entropy corresponding to AES column layout.
- **`ComputeBitSliceCorrelations(blocks [][16]byte) [128][128]float64`** (lines 303–403):
  Constructs 128x128 Pearson correlation matrix using `przm.Transpose8x8` SWAR bit unpacking.
- **`ComputeTotalBitCorrelation(corr *[128][128]float64) float64`** (lines 407–416):
  Sums off-diagonal squared correlations $\mathcal{C}_{\text{total}} = \sum_{i < j} \rho_{ij}^2$.
- **`AnalyzeStreamEntropy(data []byte) EntropyReport`** (lines 419–455):
  Master multi-dimensional evaluation pipeline returning comprehensive `EntropyReport`.

### 5.2 C Reference Codebase (`src/aes_distinguisher.c`, `include/aes_distinguisher.h`)

- **`shannon_entropy_bytes(const uint8_t *data, size_t len)`** (`aes_distinguisher.c:412–432`):
  Computes raw Shannon entropy in pure C.
- **`compute_shannon_entropy(const uint8_t *data, size_t len, double *h, double *delta_h, double *delta_h_pct)`** (`aes_distinguisher.c:434–466`):
  Evaluates entropy with Miller-Madow bias correction in C, guaranteeing 0 heap allocations.
- **`compute_chi_squared(const uint8_t *data, size_t len, double *chi2, double *z_score)`** (`aes_distinguisher.c:468–492`):
  One-pass $\chi^2$ and $Z$-score calculation over stack buffers.
- **`compute_stride4_entropy(const uint8_t *data, size_t len)`** (`aes_distinguisher.c:494–532`):
  Evaluates 65,536-bin joint distribution across 4-byte strides without dynamic memory.
- **`compute_bit_correlation(const uint8_t blocks[][16], size_t num_blocks, int bit_i, int bit_j)`** (`aes_distinguisher.c:534–574`):
  Evaluates pair correlation $\rho_{ij}$ across block arrays.
- **`evaluate_reduced_round_distinguisher(const uint8_t *data, size_t len, aes_distinguisher_report_t *report)`** (`aes_distinguisher.c:668–696`):
  Full C statistical profiling pipeline with 0 heap allocations.

---

## 6. Academic Citations & References

1. **Miller, G. A.** (1955). *Note on the bias of information estimates*. Information Theory in Psychology: Problems and Methods, II-B, pp. 95–100.
2. **Basharin, G. P.** (1959). *On a statistical estimate for the entropy of a sequence of independent random variables*. Theory of Probability & Its Applications, 4(3), pp. 333–336.
3. **Shannon, C. E.** (1948). *A Mathematical Theory of Communication*. Bell System Technical Journal, 27(3), pp. 379–423.
4. **Paninski, L.** (2003). *Estimation of entropy and mutual information*. Neural Computation, 15(6), pp. 1191–1253.
5. **NIST SP 800-22 Rev. 1a** (2010). *A Statistical Test Suite for Random and Pseudorandom Number Generators for Cryptographic Applications*. National Institute of Standards and Technology.
